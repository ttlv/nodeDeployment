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

package v1alpha1

import (
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var nodedeploymentlog = logf.Log.WithName("nodedeployment-resource")

func (r *NodeDeployment) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-edge-harmonycloud-cn-v1alpha1-nodedeployment,mutating=true,failurePolicy=fail,groups=edge.harmonycloud.cn,resources=nodedeployments,verbs=create;update,versions=v1alpha1,name=mnodedeployment.kb.io

var _ webhook.Defaulter = &NodeDeployment{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *NodeDeployment) Default() {
	nodedeploymentlog.Info("default", "name", r.Name)

	// TODO(user): fill in your defaulting logic.
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// +kubebuilder:webhook:verbs=create;update,path=/validate-edge-harmonycloud-cn-v1alpha1-nodedeployment,mutating=false,failurePolicy=fail,groups=edge.harmonycloud.cn,resources=nodedeployments,versions=v1alpha1,name=vnodedeployment.kb.io

var _ webhook.Validator = &NodeDeployment{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *NodeDeployment) ValidateCreate() error {
	nodedeploymentlog.Info("validate create", "name", r.Name)

	// TODO(user): fill in your validation logic upon object creation.
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *NodeDeployment) ValidateUpdate(old runtime.Object) error {
	nodedeploymentlog.Info("validate update", "name", r.Name)

	oldNodeDeployment, ok := old.DeepCopyObject().(*NodeDeployment)
	if !ok {
		nodedeploymentlog.Info("validate update DeepCopyObject error")
		return fmt.Errorf("do not support update operation")
	}
	if oldNodeDeployment.Spec.Platform != r.Spec.Platform ||
		oldNodeDeployment.Spec.EdgeNodeName != r.Spec.EdgeNodeName ||
		oldNodeDeployment.Spec.NodeMaintenanceName != r.Spec.NodeMaintenanceName ||
		oldNodeDeployment.Spec.ActionPolicy != r.Spec.ActionPolicy ||
		oldNodeDeployment.Spec.KubeEdgeVersion != r.Spec.KubeEdgeVersion ||
		oldNodeDeployment.Spec.CloudNodeIP != r.Spec.CloudNodeIP {
		fmt.Println("do not support update operation currently, if you want to update the object, please delete it first then re-create")
		return fmt.Errorf("do not support update operation")
	} else {
		return nil
	}
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *NodeDeployment) ValidateDelete() error {
	nodedeploymentlog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}
