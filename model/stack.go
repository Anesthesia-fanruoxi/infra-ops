package model

// StackVar 套件变量声明（共享或逐主机）。
type StackVar struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Default  string   `json:"default"`        // bool 型为 "true"/"false"
	Type     string   `json:"type,omitempty"` // "bool"=前端渲染开关；"registry"=自建仓库选择器（候选见部署中心，选中接入 hub 镜像源）；空=文本输入
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

// StackPhase 流水线阶段：由服务端（主脑）编排，逐阶段调度主机执行。
// Run 为注入 node 脚本的 {{__run}} 值，Target=all 表示全部主机、leader 表示仅首/主节点。
type StackPhase struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Target string `json:"target"` // all / leader
	// Component：本阶段归属的组件（多个用逗号分隔）。组合套件「加装组件」按此裁剪，
	// 只执行本次新增组件对应的阶段；留空表示不隶属任何组件（配合 FullOnly / Always）。
	Component string `json:"component,omitempty"`
	// FullOnly：仅整机部署（create/reinstall）执行，加装/扩容一律跳过（如 reset 清理旧环境）。
	FullOnly bool `json:"full_only,omitempty"`
	// Always：任何一次运行都执行（如收尾校验与目录初始化）。
	Always bool `json:"always,omitempty"`
}

// StackBlueprint 内置套件蓝图（只读，不落库）。
type StackBlueprint struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	RequiresDocker bool        `json:"requires_docker"` // true=Docker 容器；false=主机进程安装，不跑 Docker 预检
	Category       string      `json:"category"`        // service=单服务集群 platform=组合套件
	HaSupport      bool        `json:"ha_support"`      // 支持套件级高可用开关（前端据此渲染 el-switch）
	Modes          []StackMode `json:"modes"`
	SharedVars     []StackVar  `json:"shared_vars"`
	HostVars       []StackVar  `json:"host_vars"`
	// Extras 套件自定义的只读声明式元数据，原样下发给前端供套件自己的展示 UI 消费
	// （如 ES 的三档规格表）。骨架不认识其中任何键、不做任何分支：套件写什么、前端读什么。
	// 与部署期注入同源（同一份 Go 表），避免「界面一套数字、实际部署另一套」的漂移。
	Extras map[string]any `json:"extras,omitempty"`
}

// StackRun 一次套件部署运行（挂在集群实例上的一条流程）。
type StackRun struct {
	ID         int64   `json:"id"`
	InstanceID int64   `json:"instance_id"`
	Op         string  `json:"op"` // create/reinstall/scale_out/scale_in/add_component/uninstall/remove_component
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

// StackRunStep 套件运行的流水线步骤（一次运行内的阶段序列，前端按序渲染步骤条）。
type StackRunStep struct {
	ID         int64   `json:"id"`
	RunID      int64   `json:"run_id"`
	Seq        int     `json:"seq"`
	Key        string  `json:"key"`       // 步骤键（prereq/流水线阶段键/node/bootstrap/scale_out/remove）
	Label      string  `json:"label"`     // 展示名（取自蓝图流水线或引擎通用文案）
	Target     string  `json:"target"`    // all=全部主机 / leader=仅主节点
	Component  string  `json:"component"` // 归属组件（逗号分隔，空=不隶属）
	Phase      string  `json:"phase"`     // 日志阶段归属：prereq/node/bootstrap
	Status     string  `json:"status"`    // pending/running/success/failed/skipped
	Error      string  `json:"error"`
	StartedAt  *string `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
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
	RolePlanJSON   string              `json:"role_plan_json"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	Hosts          []StackInstanceHost `json:"hosts,omitempty"`
	HostCount      int                 `json:"host_count,omitempty"`
}

// RolePlanRole 角色计划中单个主机上的一个角色落点（docs/角色物化设计.md §2.2）。
type RolePlanRole struct {
	Comp   string `json:"comp"`            // 归属组件：hdfs/yarn/...
	Role   string `json:"role"`            // 角色键：nn1/nn2/jn/dn/...
	Label  string `json:"label"`           // 展示名：NameNode (Active)
	Source string `json:"source"`          // manual=用户指定 / auto=引擎自动落位
	Scope  string `json:"scope,omitempty"` // all=全员角色（每台成员机都有，随成员伸缩）；空=落点角色（部署后冻结）
}

// RolePlanHost 角色计划中的一台主机。
type RolePlanHost struct {
	HostID   int64          `json:"host_id"`
	HostName string         `json:"host_name"`
	HostIP   string         `json:"host_ip"`
	Seq      int            `json:"seq"`
	Roles    []RolePlanRole `json:"roles"`
}

// RolePlan 一次部署/变更操作执行前，对全部参与主机的角色落点的
// 确定性、已冻结、带来源标注的完整描述；同时是部署/探活/缩容保护/重装共读的可执行契约。
type RolePlan struct {
	Rev         int               `json:"rev"`
	Op          string            `json:"op"` // create/add_component/scale_out/scale_in/reinstall/replan
	GeneratedAt string            `json:"generated_at"`
	GeneratedBy string            `json:"generated_by"` // auto=引擎生成 / manual=用户编辑后提交
	Ha          bool              `json:"ha"`
	Hosts       []RolePlanHost    `json:"hosts"`
	Masters     map[string]string `json:"masters"`               // 权威 masters 全量投影（含自动落点的备角色），运行期推导的唯一输入
	ManualKeys  []string          `json:"manual_keys,omitempty"` // 用户显式指定的 masters 键（跨重规划保留来源标注）
	Injected    map[string]string `json:"injected,omitempty"`    // 物化后的脚本注入变量（部署直接取用）
	Warnings    []string          `json:"warnings,omitempty"`
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
