package model

// Orchestration 任务编排定义：多个「部署单元」（模板+主机+逐台变量）按顺序串行的长任务。
type Orchestration struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ExecMode    string `json:"exec_mode"` // 固定 by_step（顺序串行）
	Enabled     bool   `json:"enabled"`
	StepCount   int    `json:"step_count"` // 列表联查
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
