package model

// StackVar 套件变量声明（共享或逐主机）。
type StackVar struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Default  string   `json:"default"`
	Required bool     `json:"required"`
	Modes    []string `json:"modes,omitempty"` // 空=所有模式
}

// StackMode 套件内一种固定拓扑。
type StackMode struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	Description    string `json:"description"`
	MinHosts       int    `json:"min_hosts"`
	HostHint       string `json:"host_hint"`
	HasBootstrap   bool   `json:"has_bootstrap"`
	AssignMaster   bool   `json:"assign_master"`
	DefaultHomeDir string `json:"default_home_dir"`
}

// StackBlueprint 内置套件蓝图（只读，不落库）。
type StackBlueprint struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	RequiresDocker bool        `json:"requires_docker"` // true=Docker 容器；false=主机进程安装，不跑 Docker 预检
	Category       string      `json:"category"`        // service=单服务集群 platform=组合套件
	Modes          []StackMode `json:"modes"`
	SharedVars     []StackVar  `json:"shared_vars"`
	HostVars       []StackVar  `json:"host_vars"`
}

// StackRun 一次套件部署运行（挂在集群实例上的一条流程）。
type StackRun struct {
	ID         int64   `json:"id"`
	InstanceID int64   `json:"instance_id"`
	Op         string  `json:"op"` // create/scale_out/scale_in/add_component/uninstall/remove_component
	StackKey   string  `json:"stack_key"`
	StackName  string  `json:"stack_name"`
	Mode       string  `json:"mode"`
	Status     string  `json:"status"` // running/success/partial/failed
	Total      int     `json:"total"`
	SuccessCnt int     `json:"success_cnt"`
	FailCnt    int     `json:"fail_cnt"`
	ParamsJSON string  `json:"params_json"`
	CreatedAt  string  `json:"created_at"`
	FinishedAt *string `json:"finished_at"`
}

// StackRunHost 套件运行中的一台主机。
type StackRunHost struct {
	ID              int64   `json:"id"`
	RunID           int64   `json:"run_id"`
	HostID          int64   `json:"host_id"`
	HostName        string  `json:"host_name"`
	HostIP          string  `json:"host_ip"`
	Role            string  `json:"role"` // master/replica/node
	Seq             int     `json:"seq"`
	ParamsJSON      string  `json:"params_json"`
	Status          string  `json:"status"` // pending/running/success/failed/skipped
	PrereqStatus    string  `json:"prereq_status"`
	NodeStatus      string  `json:"node_status"`
	BootstrapStatus string  `json:"bootstrap_status"`
	Output          string  `json:"output"`
	Error           string  `json:"error"`
	StartedAt       *string `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
}

// StackRunLog 套件运行日志行。
type StackRunLog struct {
	ID        int64  `json:"id"`
	RunID     int64  `json:"run_id"`
	Phase     string `json:"phase"` // prereq/node/bootstrap
	HostID    int64  `json:"host_id"`
	HostIP    string `json:"host_ip"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// StackInstance 一套已落地的集群（可扩容/缩容/加装）。
type StackInstance struct {
	ID             int64               `json:"id"`
	Name           string              `json:"name"`
	StackKey       string              `json:"stack_key"`
	StackName      string              `json:"stack_name"`
	Mode           string              `json:"mode"`
	Category       string              `json:"category"`
	Status         string              `json:"status"` // deploying/ready/partial/failed/uninstalled
	ParamsJSON     string              `json:"params_json"`
	ComponentsJSON string              `json:"components_json"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	Hosts          []StackInstanceHost `json:"hosts,omitempty"`
	HostCount      int                 `json:"host_count,omitempty"`
}

// StackInstanceHost 集群当前成员。
type StackInstanceHost struct {
	ID             int64  `json:"id"`
	InstanceID     int64  `json:"instance_id"`
	HostID         int64  `json:"host_id"`
	HostName       string `json:"host_name"`
	HostIP         string `json:"host_ip"`
	Role           string `json:"role"`
	Seq            int    `json:"seq"`
	ParamsJSON     string `json:"params_json"`
	ComponentsJSON string `json:"components_json"`
	Status         string `json:"status"` // active/removed
}
