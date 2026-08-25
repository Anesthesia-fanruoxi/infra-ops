package model

import "encoding/json"

// DeployTemplate 部署模板：shell 正文 + 变量声明。
type DeployTemplate struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Script      string          `json:"script"`
	Variables   json.RawMessage `json:"variables"` // [{name,label,default,required}]
	IsBuiltin   bool            `json:"is_builtin"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
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
