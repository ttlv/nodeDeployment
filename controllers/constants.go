package controllers

const (
	UninstallScriptDestPath = "/root/uninstall.sh"
	ScriptPath              = "/root/scripts"
	KubeEdgeScriptsURL      = "https://edgedev.harmonycloud.online:10443/kubeedge/kubeedge/scripts/"
	User                    = "root"
	RetryTimes              = 5
	TimeoutForJoin          = 10
	Bound                   = "Bound"
	Unbound                 = "Unbound"
	DeployOnce              = "deployOnce"
	RemoveNodeFinalizerName = "remove.node.harmonycloud.cn"
)

var (
	IP       = "10.1.11.28"
	Port     = 22
	Password = "123456"
	UbuntuOSType   = "ubuntu"
	RaspbianOSType = "raspbian"
	DebianOSType   = "debian"
	CentOSType     = "centos"
)
