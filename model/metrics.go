package model

// MetricsConn 工具-监控查询：一个 Prometheus / VictoriaMetrics 查询端点配置。
type MetricsConn struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"` // prometheus | victoriametrics
	URL             string `json:"url"`
	Insecure        bool   `json:"insecure"`
	AuthType        string `json:"auth_type"` // none | basic | bearer
	Username        string `json:"username"`
	EncryptedSecret []byte `json:"-"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// MetricsConnView 连接列表展示视图（不返回任何密码材料）。
type MetricsConnView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Insecure  bool   `json:"insecure"`
	AuthType  string `json:"auth_type"`
	Username  string `json:"username"`
	HasSecret bool   `json:"has_secret"`
	Remark    string `json:"remark"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// MetricsPoint 归一化后的单个数据点：TS 为 Unix 毫秒；V 为 nil 表示该点缺失（NaN / ±Inf）。
type MetricsPoint struct {
	TS int64    `json:"ts"`
	V  *float64 `json:"v"`
}

// MetricsSeries 一条时间序列：标签集 + 按时间升序的样本点。
type MetricsSeries struct {
	Metric map[string]string `json:"metric"`
	Points []MetricsPoint    `json:"points"`
}

// MetricsQueryResult 一次查询的归一化结果。
//
// Prometheus 的 instant(vector) / range(matrix) 与 VictoriaMetrics 同根同源的响应
// 统一成这一种形态，前端只面向它绘图与统计。
type MetricsQueryResult struct {
	Expr       string          `json:"expr"`
	Mode       string          `json:"mode"`  // instant | range
	Start      int64           `json:"start"` // Unix 毫秒（instant 为查询时刻）
	End        int64           `json:"end"`
	Step       int64           `json:"step"`        // 秒；instant 为 0
	ResultType string          `json:"result_type"` // vector | matrix | scalar | string
	Series     []MetricsSeries `json:"series"`
	Warnings   []string        `json:"warnings"`
	Stats      map[string]any  `json:"stats,omitempty"` // 远端 stats 块原样透传（有则带）
	ElapsedMs  int64           `json:"elapsed_ms"`
}

// AI 调用类型（ai_logs.kind，menu=metrics）。
const (
	MetricsAIKindPromQL  = "promql"  // 自然语言生成 PromQL
	MetricsAIKindExplain = "explain" // 查询结果解读
)

// MetricsAIPromQLResult AI 生成 PromQL 的结果。
type MetricsAIPromQLResult struct {
	PromQL      string          `json:"promql"`
	Explain     string          `json:"explain"`
	UsedMetrics int             `json:"used_metrics"`           // 送入上下文的指标数
	VerifyError string          `json:"verify_error,omitempty"` // 本地试跑未通过时的报错（前端黄色提示，不阻断使用）
	Range       *MetricsAIRange `json:"range,omitempty"`        // 需求里识别出的时间范围；没提到时间为 nil
	Usage       MetricsAIUsage  `json:"usage"`
}

// MetricsAIRange AI 从需求里识别出的查询时间范围：前端填入表达式时同步切时间控件。
// Preset 命中界面预设（5m/15m/1h/6h/24h/7d）时非空，前端直接切预设 chip；
// 空串走「自定义」区间（「今天」「上周」这类日历边界与非常规窗口都落在这里）。
type MetricsAIRange struct {
	Start  int64  `json:"start"`  // Unix 毫秒
	End    int64  `json:"end"`    // Unix 毫秒
	Label  string `json:"label"`  // 可读名称，如「今天」「最近 6 小时」
	Preset string `json:"preset"` // 命中的预设标签，空串 = 自定义区间
}

// MetricsQueryDigest 查询结果的统计摘要：AI 解读的输入，同时回传前端供对照。
type MetricsQueryDigest struct {
	SeriesCount int      `json:"series_count"`
	Truncated   bool     `json:"truncated"` // 序列过多时 lines 只含前若干条
	Lines       []string `json:"lines"`     // 每序列一行可读统计
}

// MetricsAIExplainResult AI 解读查询结果。
type MetricsAIExplainResult struct {
	Summary     string             `json:"summary"`
	Findings    []string           `json:"findings"`
	Suggestions []string           `json:"suggestions"`
	Digest      MetricsQueryDigest `json:"digest"`
	Usage       MetricsAIUsage     `json:"usage"`
}

// MetricsAIUsage 单次 AI 调用的用量（下发给前端展示）。
type MetricsAIUsage struct {
	Calls            int    `json:"calls"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	Source           string `json:"source"`
	ElapsedMs        int64  `json:"elapsed_ms"`
}
