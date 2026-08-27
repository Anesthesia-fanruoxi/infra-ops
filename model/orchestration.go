package model

// Orchestration 任务记录：新建即未开始，执行一次后进入已结束（终态）。
type Orchestration struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ExecMode    string `json:"exec_mode"` // 固定 by_step（顺序串行）
	Enabled     bool   `json:"enabled"`
	StepCount   int    `json:"step_count"` // 列表联查
	State       string `json:"state"`     // 由最近一次运行派生：not_started / running / finished
	LastRunID   int64  `json:"last_run_id"`   // 最近一次运行 id，0=未运行
	Result      string `json:"result"`        // 最近运行结果：success/partial/failed，空=未运行
	OkHosts     int    `json:"ok_hosts"`      // 最近运行成功主机数
	FailHosts   int    `json:"fail_hosts"`    // 最近运行失败主机数
	TotalHosts  int    `json:"total_hosts"`   // 最近运行总主机数
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// OrchestrationStep 编排步骤 = 一个「部署单元」：模板 + 该步骤自己的主机集 + 逐台变量。
type OrchestrationStep struct {
	ID               int64  `json:"id"`
	OrchestrationID  int64  `json:"orchestration_id"`
	Seq              int    `json:"seq"`
	TemplateID       int64  `json:"template_id"`
	TemplateName     string `json:"template_name"` // 展示快照，落库在列表联查
	ParamsJSON       string `json:"params_json"`   // 步骤级默认参数
	HostScope        string `json:"host_scope"`    // JSON 数组 [hostID...]，该步骤的目标主机
	ContinueOnError  bool   `json:"continue_on_error"`
	RetryCount       int    `json:"retry_count"`
	RetryIntervalSec int    `json:"retry_interval_sec"`
	TimeoutSec       int    `json:"timeout_sec"`
}

// OrchestrationRun 编排运行实例。
type OrchestrationRun struct {
	ID              int64   `json:"id"`
	OrchestrationID int64   `json:"orchestration_id"`
	Name            string  `json:"name"`
	ExecMode        string  `json:"exec_mode"`
	Status          string  `json:"status"` // running/success/partial/failed
	TotalHosts      int     `json:"total_hosts"`
	OkHosts         int     `json:"ok_hosts"`
	FailHosts       int     `json:"fail_hosts"`
	TriggerType     string  `json:"trigger_type"`
	HostIDs         string  `json:"-"`
	CreatedAt       string  `json:"created_at"`
	FinishedAt      *string `json:"finished_at"`
}

// OrchestrationRunStep 运行明细：每主机×每步骤一行。
type OrchestrationRunStep struct {
	ID           int64   `json:"id"`
	RunID        int64   `json:"run_id"`
	HostID       int64   `json:"host_id"`
	HostName     string  `json:"host_name"`
	HostIP       string  `json:"host_ip"`
	Seq          int     `json:"seq"`
	TemplateID   int64   `json:"template_id"`
	TemplateName string  `json:"template_name"`
	Status       string  `json:"status"` // pending/running/success/failed/skipped
	Attempt      int     `json:"attempt"`
	Output       string  `json:"output"`
	Error        string  `json:"error"`
	StartedAt    *string `json:"started_at"`
	FinishedAt   *string `json:"finished_at"`
}

// OrchestrationStepVar 步骤内某台主机的变量覆盖（主机级）。
type OrchestrationStepVar struct {
	Seq        int    `json:"seq"`
	HostID     int64  `json:"host_id"`
	ParamsJSON string `json:"params_json"`
}

// OrchestrationRunLog 运行日志行：按步骤落库，供详情流快照重放与回溯查看。
type OrchestrationRunLog struct {
	ID        int64  `json:"id"`
	RunID     int64  `json:"run_id"`
	Seq       int    `json:"seq"`
	HostID    int64  `json:"host_id"`
	HostIP    string `json:"host_ip"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}
