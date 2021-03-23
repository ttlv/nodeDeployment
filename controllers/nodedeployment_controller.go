/*
Copyright 2020 The HarmonyCloud authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	nodemaintenance "github.com/ttlv/nodemaintenances/api/v1alpha1"
	edgev1alpha1 "nodedeployment/api/v1alpha1"
)

// NodeDeploymentReconciler reconciles a NodeDeployment object
type NodeDeploymentReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// 新增
// +kubebuilder:rbac:groups=edge.harmonycloud.cn,resources=nodedeployment,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=edge.harmonycloud.cn,resources=nodedeployment/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=edge.harmonycloud.cn,resources=nodemaintenance,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=edge.harmonycloud.cn,resources=nodemaintenance/status,verbs=get;update;patch

// +kubebuilder:rbac:groups=apps,resources=deployment,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=service,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secret,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pod,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=node,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=event,verbs=get;list;watch;create;update;patch;delete

func (r *NodeDeploymentReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	// 获取 NodeDeployment 对象实例
	nd := &edgev1alpha1.NodeDeployment{}
	if err := r.Get(ctx, req.NamespacedName, nd); err != nil {
		log.Error(err, fmt.Sprintf("Unable to fetch NodeDeployment [%s]", nd.Name))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 下线操作(云端删除节点，以及与之相关联的资源；边缘端停止edgecore进程，以及删除相关联的资源)
	// finalizer，当删除 NodeDeployment 对象时，执行节点下线操作
	removeNodeFinalizer := RemoveNodeFinalizerName
	// 通过检查 DeletionTimestamp 是否为 0 来判断资源是否正在被删除
	if nd.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(nd.ObjectMeta.Finalizers, removeNodeFinalizer) {
			nd.ObjectMeta.Finalizers = append(nd.ObjectMeta.Finalizers, removeNodeFinalizer)
			// 更新NodeDeployment对象，注意，这里不是r.Status().Update(), 而是r.Update(), 因为这里更新的是ObjectMeta
			if err := r.Update(ctx, nd); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if containsString(nd.ObjectMeta.Finalizers, removeNodeFinalizer) {
			if err := r.deleteExternalResources(req, nd); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}
			// remove our finalizer from the list and update it.
			nd.ObjectMeta.Finalizers = removeString(nd.ObjectMeta.Finalizers, removeNodeFinalizer)
			if err := r.Update(ctx, nd); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	// 根据 NodeDeployment 获取对应的 NodeMaintenance 信息
	nm, err := r.fetchNodeMaintenance(nd)
	if err != nil {
		msg := fmt.Sprintf("can't get %s", nm.Name)
		log.V(1).Info(msg)
		return ctrl.Result{}, nil
	}

	// 如果 NodeMaintenance 对象不可用，直接返回
	// Condition是一个数组，暂时只获取第一个元素（2020-09-24）
	if nm.Status.Conditions[0].Status == false {
		msg := fmt.Sprintf("%s is unable to use", nm.Name)
		log.V(1).Info(msg)
		return ctrl.Result{}, nil
	}

	// 如果 NodeMaintenance 对象可用，则检查 NodeMaintenance 与该 NodeDeployment 的绑定关系
	// 1.如果没有绑定，则绑定该 NodeDeployment，同时更新 NodeMaintenance 的绑定状态
	if nm.Status.BindStatus.Phase == Unbound || nm.Status.BindStatus.Phase == "" {
		nm.Status.BindStatus.Phase = Bound
		nm.Status.BindStatus.NodeDeploymentReference = nd.Name
		nm.Status.BindStatus.TimeStamp = currentTime()

		if err := r.Status().Update(ctx, nm); err != nil {
			log.Error(err, "Unable to update NodeMaintenance status")
			return ctrl.Result{}, nil
		}
	} else { // 2.如果已经绑定，则查看绑定的 NodeDeployment 是否为自己
		if nm.Status.BindStatus.NodeDeploymentReference != nd.Name {
			message := fmt.Sprintf("NodeMaintenance [%s] has been referenced by other NodeDeployment", nd.Spec.NodeMaintenanceName)
			r.Recorder.Event(nd, corev1.EventTypeWarning, "NodeMaintenanceBeReferenced", message)
			log.V(1).Info(message)
			return ctrl.Result{}, nil
		}
	}
	// 根据 NodeDeployment 查询对应的节点
	IP, Port = getInfoFromNodeMaintenance(nm)
	edgeNode := nd.Spec.EdgeNodeName
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "", Name: edgeNode}, node); err != nil { // 节点不在集群中
		log.V(1).Info(fmt.Sprintf("Unable to fetch node [%s]", edgeNode))

		// 更新NodeDeployment状态，表示正在执行节点上线流程
		// 这么做存疑！（2020-11-23）
		nd.Status.Message = "edge node joining"
		nd.Status.State = "Pending"
		if err := r.Status().Update(context.Background(), nd); err != nil {
			return ctrl.Result{}, nil
		}

		// 执行节点上线
		if err := Join(r, req, nd); err != nil {
			// 上线失败，事件上报
			failedMsg := fmt.Sprintf("[%s] join failed", edgeNode)
			r.Recorder.Event(nd, corev1.EventTypeWarning, "JoinFailed", failedMsg)
			log.V(1).Info(failedMsg)
			// 更新NodeDeployment状态，表示节点上线失败
			nd.Status.Message = "edge node joined failed"
			nd.Status.State = "Failed"
			if err := r.Status().Update(context.Background(), nd); err != nil {
				// 这里若更新失败，不做处理
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, nil
		}

		// 至此，表示节点上线成功
		succeedMsg := fmt.Sprintf("[%s] join successfully", edgeNode)
		r.Recorder.Event(nd, corev1.EventTypeNormal, "JoinSucceed", succeedMsg)
		log.V(1).Info(succeedMsg)

		// 更新NodeDeployment状态，表示节点上线成功
		nd.Status.Message = "edge node joined successfully"
		nd.Status.State = "Succeed"
		if err := r.Status().Update(context.Background(), nd); err != nil {
			// 这里若更新失败，不做处理
			return ctrl.Result{}, nil
		}
	} else { // 节点存在集群中
		msg := fmt.Sprintf("Node [%s] already exist, it's status is %s", node.Name, string(node.Status.Conditions[0].Type))
		log.V(1).Info(msg)
		// 如果执行节点上线的策略是 DeployOnce，则直接退出（暂时只设计了这一种上线策略）
		// 现在，我们只考虑节点上线，不考虑edgecore挂掉的情况（暂时不维护这种情况，由用户自己维护）
		if nd.Spec.ActionPolicy == DeployOnce {
			return ctrl.Result{}, nil
		} else {
			// unreachable now(2020-09-17)
		}
	}

	return ctrl.Result{}, nil
}

func (r *NodeDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&edgev1alpha1.NodeDeployment{}).
		Complete(r)
}

// deleteExternalResources delete any external resources associated with the NodeDeployment,
// here we execute node dis-join operation
// Ensure that delete implementation is idempotent(幂等性) and safe to invoke multiple types for same object.
func (r *NodeDeploymentReconciler) deleteExternalResources(req ctrl.Request, nd *edgev1alpha1.NodeDeployment) error {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)
	// 先在云端控制面检查是否存在待删除的节点，如果不存在，则直接退出；如果存在，则执行节点下线动作
	node := &corev1.Node{}
	// 待删除节点不存在
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "", Name: nd.Spec.EdgeNodeName}, node); err != nil {
		msg := fmt.Sprintf("[%s] doesn't exist", nd.Spec.EdgeNodeName)
		log.V(1).Info(msg, "err", err)
		return nil
	}
	// 待删除节点存在
	log.V(1).Info("now start deleting edge node...")
	if err := DisJoin(r, req, nd); err != nil {
		log.Error(err, "DisJoin error")
		return err // 返回err，表示节点下线失败，会再次执行finalizer
	}
	return nil
}

// helper functions
func getInfoFromNodeMaintenance(nm *nodemaintenance.NodeMaintenance) (ip string, port int) {
	ip = nm.Spec.Services[0].FrpServerIpAddress
	port, _ = strconv.Atoi(nm.Spec.Services[0].ProxyPort)
	return
}

func (r *NodeDeploymentReconciler) fetchNodeMaintenance(nd *edgev1alpha1.NodeDeployment) (*nodemaintenance.NodeMaintenance, error) {
	nm := &nodemaintenance.NodeMaintenance{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "", Name: nd.Spec.NodeMaintenanceName}, nm); err != nil {
		message := fmt.Sprintf("Unable to fetch NodeMaintenance [%s]", nd.Spec.NodeMaintenanceName)
		r.Recorder.Event(nd, corev1.EventTypeWarning, "NodeMaintenanceNoExist", message)
		return nil, err
	} else {
		return nm, nil
	}
}

func currentTime() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
