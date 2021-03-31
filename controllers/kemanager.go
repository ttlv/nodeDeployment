package controllers

import (
	"context"
	"errors"
	"fmt"
	edgev1alpha1 "github.com/ttlv/nodedeployment/api/v1alpha1"
	"github.com/ttlv/nodedeployment/constant"
	"github.com/ttlv/nodedeployment/utils"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"time"
)

/**
sshClient 接口由 FRP侧 提供
*/

func Join(r *NodeDeploymentReconciler, req ctrl.Request, nd *edgev1alpha1.NodeDeployment) error {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	// 执行节点上线
	if err := doJoin(r, req, nd); err != nil {
		return err
	}

	// 到这里，节点上线的3个步骤正常执行完成，但不代表 edgecore 就立刻连入集群
	// 需要等待一定时间(默认10秒)，检查 cloud 端能否正常获取到新加入的节点信息，当且仅当新节点已加入并且状态为 Ready 才表示节点上线成功
	time.Sleep(constant.TimeoutForJoin * time.Second)

	node := &corev1.Node{}
	// 新节点不在集群中
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "", Name: nd.Spec.EdgeNodeName}, node); err != nil {
		msg := fmt.Sprintf("still cannot join [%s] into cluster even the script execute successfully", nd.Spec.EdgeNodeName)
		log.V(1).Info(msg)
		return err
	}
	// 新节点在集群中，但是状态为 NotReady
	if node.Status.Conditions[0].Status == "False" || node.Status.Conditions[0].Status == "Unknown" {
		msg := fmt.Sprintf("%s", nd.Spec.EdgeNodeName+" exist, but NotReady")
		log.V(1).Info(msg)
		return errors.New(msg)
	}

	return nil
}

func DisJoin(r *NodeDeploymentReconciler, req ctrl.Request, nd *edgev1alpha1.NodeDeployment) error {
	if err := doDisJoin(r, req, nd); err != nil {
		// 节点下线失败，事件上报
		failedMsg := fmt.Sprintf("[%s] disjoin failed", nd.Spec.EdgeNodeName)
		r.Recorder.Event(nd, corev1.EventTypeWarning, "DisjoinError", failedMsg)
		return err
	}
	// 节点下线成功，事件上报
	succeedMsg := fmt.Sprintf("[%s] disjoin succeed", nd.Spec.EdgeNodeName)
	r.Recorder.Event(nd, corev1.EventTypeNormal, "DisjoinSucceed", succeedMsg)

	return nil
}

func doJoin(r *NodeDeploymentReconciler, req ctrl.Request, nd *edgev1alpha1.NodeDeployment) error {
	sshClient, err := createSSHClient(r, req, constant.User, constant.IP, constant.Password, constant.Port)
	if err != nil {
		message := "[stage: join], create ssh client via FRP failed"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "NetworkError", message)
		return err
	}
	message := "[stage: join], create ssh client via FRP succeeded"
	r.Recorder.Event(nd, corev1.EventTypeNormal, "NetworkNormal", message)

	// 首先判断docker是否正常安装
	if !existDocker(r, req, sshClient) {
		message := "[stage: join], docker doesn't exist"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "DockerNoExist", message)
		return errors.New("docker doesn't exist")
	} else {
		message := "[stage: join], docker exist"
		r.Recorder.Event(nd, corev1.EventTypeNormal, "DockerExist", message)
	}

	// 然后判断keadm是否安装，若未安装，则安装之
	if !existKeadm(r, req, sshClient) {
		if err := downloadKeadm(r, nd, req, sshClient); err != nil {
			message := "[stage: join], keadm doesn't exist and download failed"
			r.Recorder.Event(nd, corev1.EventTypeWarning, "KeadmNoExist", message)
			return errors.New("keadm doesn't exist")
		}
	} else {
		message := "[stage: join], keadm exist"
		r.Recorder.Event(nd, corev1.EventTypeNormal, "KeadmExist", message)
	}

	err = downloadScript(r, req, sshClient)
	if err != nil {
		message := "[stage: join], download script from server failed"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "DownloadError", message)
		return err
	} else {
		message := "[stage: join], download script from server succeed"
		r.Recorder.Event(nd, corev1.EventTypeNormal, "DownloadNormal", message)
	}

	err = executeJoinScript(r, req, nd, sshClient)
	if err != nil {
		message := "[stage: join], execute the installation script failed"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "ExecError", message)
		return err
	} else {
		message := "[stage: join], execute the installation script succeeded"
		r.Recorder.Event(nd, corev1.EventTypeNormal, "ExecNormal", message)
	}
	return nil
}

func doDisJoin(r *NodeDeploymentReconciler, req ctrl.Request, nd *edgev1alpha1.NodeDeployment) error {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	sshClient, err := createSSHClient(r, req, constant.User, constant.IP, constant.Password, constant.Port)
	if err != nil {
		msg := "[stage: disjoin], create ssh client via FRP failed"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "NetworkError", msg)
		return err
	} else {
		msg := "[stage: disjoin], create ssh client via FRP succeeded"
		r.Recorder.Event(nd, corev1.EventTypeNormal, "NetworkNormal", msg)
	}

	if err := cleanup(r, req, sshClient); err != nil {
		msg := "[stage: disjoin], clean up on edge node failed"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "CleanUpError", msg)
		return err
	}
	msg := "[stage: disjoin], clean up on edge node succeeded"
	r.Recorder.Event(nd, corev1.EventTypeNormal, "CleanUpNormal", msg)

	// 执行删除ND操作时，将当前与ND绑定的NM对象的绑定状态修改为"Unbound"
	nm, err := r.fetchNodeMaintenance(nd)
	if err != nil {
		return err
	}
	nm.Status.BindStatus.Phase = constant.Unbound
	nm.Status.BindStatus.NodeDeploymentReference = ""
	nm.Status.BindStatus.TimeStamp = currentTime()

	if err := r.Status().Update(context.Background(), nm); err != nil {
		log.Error(err, "unable to update nodemaintenance", "nodemaintenance", nm)
		msg := "[stage: disjoin], update nodemaintenance to `Unbound` failed"
		r.Recorder.Event(nd, corev1.EventTypeWarning, "UpdateNMError", msg)
		return err
	}
	msg = "[stage: disjoin], update nodemaintenance to `Unbound` succeeded"
	r.Recorder.Event(nd, corev1.EventTypeNormal, "UpdateNMNormal", msg)

	nodes := &corev1.NodeList{}
	if err := r.List(context.Background(), nodes); err != nil {
		log.Error(err, "unable to list node", "nodeList", nodes)
		return err
	}

	for _, node := range nodes.Items {
		//fmt.Printf("node name: %s\n", node.Name)
		if node.Name == nd.Spec.EdgeNodeName {
			if err := r.Delete(context.Background(), &node); client.IgnoreNotFound(err) != nil {
				log.Error(err, "unable to delete node", "node", node)
				msg := "[stage: disjoin], delete node failed"
				r.Recorder.Event(nd, corev1.EventTypeWarning, "DeleteNodeError", msg)
				return err
			} else {
				log.V(0).Info("delete node successfully", "node", node)
				msg = "[stage: disjoin], delete node succeeded"
				r.Recorder.Event(nd, corev1.EventTypeNormal, "DeleteNodeNormal", msg)
			}
		}
	}

	return nil
}

// createSSHClient create SSH client for login remote edge node
func createSSHClient(r *NodeDeploymentReconciler, req ctrl.Request, user, ip, password string, port int) (*ssh.Client, error) {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	for i := 0; i < constant.RetryTimes; i++ {
		// TODO: it is NOT safe here, please use util.CreateClientByKey(rsaPath, user, host, port)
		// for a new edge node, how to deal with rsa file?
		if sshClient, err := util.CreateClientByPassword(user, ip, password, port); err != nil {
			log.Error(err, "Unable to create ssh client")
			log.V(1).Info("Unable to create ssh client, try again")
		} else {
			return sshClient, nil
		}
	}
	log.V(1).Info("After try " + strconv.Itoa(constant.RetryTimes) + " times, still unable to create ssh client")
	return nil, errors.New("create ssh client error")
}

// downloadScript download install script from server
func downloadScript(r *NodeDeploymentReconciler, req ctrl.Request, sshClient *ssh.Client) error {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	for i := 0; i < constant.RetryTimes; i++ {
		// cmd := "wget -k --no-check-certificate https://edgedev.harmonycloud.online:10443/kubeedge/kubeedge/scripts/install.sh"

		cmd := fmt.Sprintf("mkdir -p %s && cd %s && rm -f * && sudo wget -k --no-check-certificate %s%s", constant.ScriptPath, constant.ScriptPath, constant.KubeEdgeScriptsURL, "install.sh")
		if err := util.Run(sshClient, cmd); err != nil {
			log.Error(err, "Unable to download script from server")
			log.V(1).Info("Unable to copy script from server, try again")
		} else {
			return nil
		}
	}
	log.V(1).Info("After try " + strconv.Itoa(constant.RetryTimes) + " times, still unable to download script from server")
	return errors.New("download script from server error")
}

// executeJoinScript execute install scripts for joining edge node into the cluster
func executeJoinScript(r *NodeDeploymentReconciler, req ctrl.Request, nd *edgev1alpha1.NodeDeployment, sshClient *ssh.Client) error {
	ctx := context.Background()
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	// get token via secret
	// kubectl get secret tokensecret -n kubeedge -o yaml
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: "kubeedge", Name: "tokensecret"}, secret); err != nil {
		log.Error(err, "Unable to get token")
		return err
	}
	token := string(secret.Data["tokendata"])
	cloudNodeIP := nd.Spec.CloudNodeIP
	version := nd.Spec.KubeEdgeVersion
	edgeNodeName := nd.Spec.EdgeNodeName
	command := fmt.Sprintf("/bin/bash %s/%s %s %s %s %s", constant.ScriptPath, "install.sh", cloudNodeIP, version, token, edgeNodeName)
	log.V(1).Info("start installing edge node...")
	for i := 0; i < constant.RetryTimes; i++ {
		if out, err := util.RunWithOutput(sshClient, command); err != nil {
			log.Error(err, "Unable to run command on edge")
			log.V(1).Info("Unable to run command on edge, try again")
		} else {
			log.V(1).Info(out) //
			return nil
		}
	}
	log.V(1).Info("After try " + strconv.Itoa(constant.RetryTimes) + " times, still unable to run command on edge")
	return errors.New("execute scripts error")
}

// cleanup delete scripts and ca and cert on edge node when dis-join node
func cleanup(r *NodeDeploymentReconciler, req ctrl.Request, sshClient *ssh.Client) error {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	log.V(1).Info("clean up on edge node...")
	command := "keadm reset"
	if _, err := util.RunWithOutput(sshClient, command); err != nil {
		msg := fmt.Sprintf("run command %s on edge failed", command)
		log.Error(err, msg)
		return err
	}

	command = "rm -rf /root/scripts/* && cd /etc/kubeedge && rm -rf ca/ certs/ && rm -rf edgecore*"
	if _, err := util.RunWithOutput(sshClient, command); err != nil {
		msg := fmt.Sprintf("run command %s on edge failed", command)
		log.Error(err, msg)
		return err
	}
	return nil
}

// existKeadm determines whether keadm exists or not on edge node
func existKeadm(r *NodeDeploymentReconciler, req ctrl.Request, sshClient *ssh.Client) bool {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)
	cmd := "ls /usr/local/bin/keadm"
	if err := util.Run(sshClient, cmd); err != nil {
		log.V(1).Info("keadm doesn't exist on to-be-joined node", "err", err)
		return false
	} else {
		log.V(1).Info("keadm exist on to-be-joined node")
		return true
	}
}

// downloadKeadm download keadm(modified by Harmony Cloud) from server
func downloadKeadm(r *NodeDeploymentReconciler, nd *edgev1alpha1.NodeDeployment, req ctrl.Request, sshClient *ssh.Client) error {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)

	// 根据边缘节点架构选择对应的keadm
	arch, err := getSystemArch(sshClient)
	if err != nil {
		return err
	}

	for i := 0; i < constant.RetryTimes; i++ {
		// cmd := "sudo wget -k --no-check-certificate https://edgedev.harmonycloud.online:10443/kubeedge/kubeedge/keadm-hc/keadm-v1.4.0-linux-arm64/keadm"
		cmd := fmt.Sprintf("sudo wget -k --no-check-certificate https://edgedev.harmonycloud.online:10443/kubeedge/kubeedge/keadm-hc/keadm-v%s-linux-%s/keadm && chmod +x keadm && cp keadm /usr/local/bin/", nd.Spec.KubeEdgeVersion, arch)
		if err := util.Run(sshClient, cmd); err != nil {
			log.Error(err, "Unable to download keadm")
			log.V(1).Info("Unable to download keadm, try again")
		} else {
			return nil
		}
	}
	log.V(1).Info("After try " + strconv.Itoa(constant.RetryTimes) + " times, still unable to download keadm")
	return errors.New("download keadm error")
}

func getSystemArch(sshClient *ssh.Client) (string, error) {
	arch := "amd64"
	// 先判断节点的系统是属于Ubuntu、CentOS、Raspbian、Debian
	OSType, err := GetOSInterface(sshClient)
	if err != nil {
		return "", err
	}
	// 然后根据不同的系统，检测位数（arm, arm64, amd64）
	switch OSType {
	case "ubuntu":
		arch, err = getSystemArchUbuntu(sshClient)
		if err != nil {
			return "", err
		}
	case "centos":
		arch, err = getSystemArchCentos(sshClient)
		if err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("wrong arch")
	}
	return arch, nil
}

func getSystemArchCentos(sshClient *ssh.Client) (string, error) {
	arch := "amd64"
	out, err := util.RunWithOutput(sshClient, "arch")
	if err != nil {
		return "", err
	}

	switch string(out) {
	case "armv7l":
		arch = "arm"
	case "aarch64":
		arch = "arm64"
	case "x86_64":
		arch = "amd64"
	default:
		return "", fmt.Errorf("can't support this architecture of CentOS: %s", out)
	}
	return arch, nil
}

func getSystemArchUbuntu(sshClient *ssh.Client) (string, error) {
	out, err := util.RunWithOutput(sshClient, "dpkg --print-architecture")
	if err != nil {
		return "", err
	}
	return out, nil
}

func GetOSInterface(sshClient *ssh.Client) (string, error) {
	OS, err := GetOSVersion(sshClient)
	if err != nil {
		return "", err
	}
	fmt.Printf("edge node's OS type is [%s]\n", OS)
	switch OS {
	case constant.UbuntuOSType, constant.RaspbianOSType, constant.DebianOSType:
		return "ubuntu", nil //
	case constant.CentOSType:
		return "centos", nil //
	default:
		fmt.Printf("This OS version is currently un-supported by keadm, %s", OS)
		panic("This OS version is currently un-supported by keadm")
	}
}

func GetOSVersion(sshClient *ssh.Client) (string, error) {
	cmd := ". /etc/os-release && echo $ID"
	OSType, err := util.RunWithOutput(sshClient, cmd)
	if err != nil {
		return "", err
	}
	return OSType, nil
}

// existDocker determines whether docker exists or not on edge node needed to be joined
func existDocker(r *NodeDeploymentReconciler, req ctrl.Request, sshClient *ssh.Client) bool {
	log := r.Log.WithValues("nodedeployment", req.NamespacedName)
	cmd := "docker -v"
	if err := util.Run(sshClient, cmd); err != nil {
		log.V(1).Info("Docker doesn't exist on to-be-joined node", "err", err)
		return false
	} else {
		log.V(1).Info("Docker exist on to-be-joined node")
		return true
	}
}
