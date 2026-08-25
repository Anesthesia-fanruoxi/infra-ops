// Package model 定义全局数据结构，无业务方法。
package model

// Host 主机记录。
type Host struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	Tag          string `json:"tag"`
	Remark       string `json:"remark"`
	CredentialID int64  `json:"credential_id"`
	Status       string `json:"status"`
	LatencyMs    int    `json:"latency_ms"`
	InfoJSON     string `json:"info_json"`
	LastCheckAt  string `json:"last_check_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// HostInfo 采集到的主机资源快照（解析自 info_json）。
type HostInfo struct {
	Hostname       string     `json:"hostname"`
	OS             string     `json:"os"`
	Kernel         string     `json:"kernel"`
	UptimeHours    float64    `json:"uptime_hours"`
	CPUCores       int        `json:"cpu_cores"`
	Load1          float64    `json:"load1"`
	MemTotalMB     int        `json:"mem_total_mb"`
	MemUsedPercent int        `json:"mem_used_percent"`
	Disk           []DiskInfo `json:"disk"`
}

type DiskInfo struct {
	Mount       string  `json:"mount"`
	SizeGB      float64 `json:"size_gb"`
	UsedPercent int     `json:"used_percent"`
}

// HostInstall 主机安装标记：该主机成功执行过的安装模板（每模板一条，重复执行刷新时间）。
type HostInstall struct {
	ID           int64  `json:"id"`
	HostID       int64  `json:"host_id"`
	TemplateID   int64  `json:"template_id"`
	TemplateName string `json:"template_name"`
	TaskID       int64  `json:"task_id"`
	CreatedAt    string `json:"created_at"` // 首次安装时间
	UpdatedAt    string `json:"updated_at"` // 最近一次执行时间
}
