package model

// 平台设置的模型定义：AI 接入与系统信息。

// AIConfig AI 接入配置（下发给前端时只暴露是否已配置密钥，密钥内容永不出库）。
type AIConfig struct {
	Enabled   bool         `json:"enabled"`
	Provider  string       `json:"provider"`
	Providers []AIProvider `json:"providers"` // 可选接入点清单（内置预设，含派生地址）
	BaseURL   string       `json:"base_url"`
	Model     string       `json:"model"`
	HasKey    bool         `json:"has_key"`
	Timeout   int          `json:"timeout"`
}

// AIProvider 一个内置接入点预设：选定后接口地址由平台派生，用户只需填密钥。
// 同一厂商可有多个接入点（如小米 MiMo 的按量付费与 Token Plan 分属不同集群）。
type AIProvider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	KeyHint string `json:"key_hint"` // 密钥格式提示（如 sk-xxxx / tp-xxxxx）
}

// AIModelList 拉取到的可用模型清单。
// 与连通性测试同理：失败是业务结果，以 200 + ok=false 返回，前端好区分「取不到」与「请求出错」。
type AIModelList struct {
	OK     bool     `json:"ok"`
	Error  string   `json:"error"`
	Models []string `json:"models"`
}

// AITestResult AI 连通性测试结果。
// 失败是业务结果而非请求错误，故以 200 + ok=false 返回，便于前端区分「网络不通」与「接口报错」。
type AITestResult struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	Model       string `json:"model"` // 服务端回显的模型名（多模型网关下比配置值更准）
	Reply       string `json:"reply"` // 模型返回文本（已截断）
	LatencyMs   int64  `json:"latency_ms"`
	TotalTokens int    `json:"total_tokens"`
	TokenSource string `json:"token_source"` // api / estimated
}

// TableStat 单表行数。
type TableStat struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// SystemInfo 系统信息（只读，供排查问题用）。
type SystemInfo struct {
	Version        string      `json:"version"`
	GoVersion      string      `json:"go_version"`
	StartedAt      string      `json:"started_at"`
	UptimeSec      int64       `json:"uptime_sec"`
	WorkDir        string      `json:"work_dir"`
	DBPath         string      `json:"db_path"`
	DBSizeBytes    int64       `json:"db_size_bytes"`
	Tables         []TableStat `json:"tables"`
	TableTotalRows int64       `json:"table_total_rows"`
}
