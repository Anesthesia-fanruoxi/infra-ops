package model

import "encoding/json"

// DeployTemplate 部署模板：shell 正文 + 变量声明 + 分类/前置依赖。
type DeployTemplate struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"` // 功能分类：系统/工具/中间件/…（内置/自定义另有 is_builtin 徽标，二者并存）
	Script      string          `json:"script"`
	Variables   json.RawMessage `json:"variables"` // [{name,label,default,required}]
	Services    json.RawMessage `json:"services"`  // [{name,url,web}]
	Requires    json.RawMessage `json:"requires"`  // [{check,hint}] 前置依赖检查
	Configs     json.RawMessage `json:"configs"`   // [{key,label,file,hint,required}] 可被用户覆盖的配置文件
	IsBuiltin   bool            `json:"is_builtin"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// TemplateConfig 模板中声明的一个「可由用户覆盖」的配置文件。
// 部署时若提供了对应 key 的自定义内容，渲染器会将其覆盖写入 file（脚本中需用锚点
// # __DEPLOY_CONF__ <key> ... # __DEPLOY_CONF_END__ <key> 包裹默认写入块，未提供则保持默认）。
type TemplateConfig struct {
	Key      string `json:"key"`      // 唯一标识，如 nginx_conf / my_cnf
	Label    string `json:"label"`    // 展示名
	File     string `json:"file"`     // 目标文件路径，可含 {{var}} 占位（如 {{data_dir}}/my.cnf）
	Hint     string `json:"hint"`     // 输入提示（占位/说明）
	Required bool   `json:"required"` // 是否必填自定义内容
}

// TemplateService 模板声明的服务；url 支持 {{ip}} 与 {{变量名}} 占位符，安装成功登记时替换。
type TemplateService struct {
	Name string `json:"name"`
	URL  string `json:"url"` // 占位符模板，如 http://{{ip}}:{{port}}
	Web  bool   `json:"web"` // true 提供"打开"按钮
}

// TemplateDependency 模板前置依赖检查：在目标主机执行 check，非 0 则阻断并提示 hint。
type TemplateDependency struct {
	Check string `json:"check"` // 在目标主机 $SHELL 下执行的单行判断，如 command -v docker
	Hint  string `json:"hint"`  // 不满足时的提示文案
}

// DeployTask 一次批量部署任务。
type DeployTask struct {
	ID           int64   `json:"id"`
	TemplateID   int64   `json:"template_id"`
	TemplateName string  `json:"template_name"`
	Status       string  `json:"status"` // running/success/partial/failed
	Total        int     `json:"total"`
	SuccessCnt   int     `json:"success_cnt"`
	FailCnt      int     `json:"fail_cnt"`
	TriggerType  string  `json:"trigger_type"` // manual/schedule
	ScheduleID   int64   `json:"schedule_id"`  // 定时触发时的 schedule ID，手动为 0
	ParamsJSON   string  `json:"params_json"`  // 任务级默认变量(JSON)，空为"{}"
	CreatedAt    string  `json:"created_at"`
	FinishedAt   *string `json:"finished_at"`
}

// DeploySchedule 定时部署任务。
type DeploySchedule struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	TemplateID int64           `json:"template_id"`
	HostIDs    json.RawMessage `json:"host_ids"` // [1,2,3]
	Params     json.RawMessage `json:"params"`   // {"k":"v"}
	CronExpr   string          `json:"cron_expr"`
	Enabled    bool            `json:"enabled"`
	LastTaskID int64           `json:"last_task_id"`
	LastRunAt  *string         `json:"last_run_at"`
	NextRunAt  *string         `json:"next_run_at"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

// DeployTaskHost 任务中单台主机的执行记录。
type DeployTaskHost struct {
	ID         int64   `json:"id"`
	TaskID     int64   `json:"task_id"`
	HostID     int64   `json:"host_id"`
	HostName   string  `json:"host_name"`
	HostIP     string  `json:"host_ip"`
	Status     string  `json:"status"` // pending/running/success/failed
	Output     string  `json:"output"`
	Error      string  `json:"error"`
	StartedAt  *string `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
	ParamsJSON string  `json:"params_json"` // 该主机的变量覆盖(JSON)，空为"{}"
}

// DeployLog 部署任务运行时日志行：先落库再发布，供日志抽屉快照回放与实时追加。
type DeployLog struct {
	ID        int64  `json:"id"`
	TaskID    int64  `json:"task_id"`
	HostID    int64  `json:"host_id"`
	HostIP    string `json:"host_ip"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}
