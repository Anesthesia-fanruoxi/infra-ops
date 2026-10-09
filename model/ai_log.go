// ai_log.go 统一 AI 调用记录：所有工具把 AI 调用写进同一张 ai_logs 表，
// 用 menu 区分归属工具；各工具视图按菜单过滤展示，选中连接时叠加 conn_id。
package model

// AI 调用记录的菜单归属（ai_logs.menu）—— 存页面 id（前端路由表的键），
// 展示名由前端映射：页面名称会改（如「操作日志」→「审计日志」），
// id 才是稳定的关联键（与 audit_logs.target_type 存页面 id 同一口径）。
const (
	AIMenuMetrics = "metrics" // 监控查询
	AIMenuMySQL   = "mysql"   // MySQL 工作台
)

// AI 调用记录的类型、状态与 token 来源取值（后端、前端、筛选参数共用同一套口径）。
const (
	AILogKindCatalog = "catalog" // MySQL：归纳表语义目录
	AILogKindSQL     = "sql"     // MySQL：自然语言生成 SQL
	// MySQL：解读 EXPLAIN 执行计划（与监控查询的结果解读同值，按 menu 区分归属）
	AILogKindExplain = "explain"

	LogStatusOK    = "ok"
	LogStatusError = "error"

	TokenSourceAPI       = "api"       // 用量来自接口返回的 usage
	TokenSourceEstimated = "estimated" // 接口没回 usage，按字符估算
)

// AILog 一条 AI 调用的记录（含 token 用量）。
//
// 各工具的字段是并集，按 menu 取用：MySQL 的记录带 schema / mode / batch_no /
// batch_total / table_count（catalog 按批各一条）；监控查询的记录带 metric_count
// 与 expr（生成出的 PromQL）。
type AILog struct {
	ID               int64  `json:"id"`
	Menu             string `json:"menu"` // 页面 id：metrics / mysql
	ConnID           int64  `json:"conn_id"`
	ConnName         string `json:"conn_name"`
	SchemaName       string `json:"schema"`
	Kind             string `json:"kind"`  // promql / explain / catalog / sql
	Mode             string `json:"mode"`  // 生成时会话模式（read / write），目录生成时为空
	Model            string `json:"model"` // 服务端回显的模型名，缺失时回落配置值
	BaseURL          string `json:"base_url"`
	BatchNo          int    `json:"batch_no"`     // 语义目录的第几批（1 起）
	BatchTotal       int    `json:"batch_total"`  // 本轮共几批
	TableCount       int    `json:"table_count"`  // 本次送进模型的表数量
	MetricCount      int    `json:"metric_count"` // 本次送进模型的指标数
	Question         string `json:"question"`     // 用户原话
	Expr             string `json:"expr"`         // 生成出的表达式 / SQL（目录记录为空）
	Content          string `json:"content"`      // 模型返回原文（目录记录存归纳摘要）
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	TokenSource      string `json:"token_source"` // api / estimated
	ElapsedMs        int64  `json:"elapsed_ms"`
	Status           string `json:"status"` // ok / error
	Error            string `json:"error"`
	CreatedAt        string `json:"created_at"`
}

// AILogFilter AI 调用记录的查询条件（零值 = 不过滤）。
type AILogFilter struct {
	Menu     string // 页面 id：metrics / mysql / 空=全部
	ConnID   int64  // 0 = 全部连接
	Kind     string // promql / explain / catalog / sql / 空=全部
	Status   string // ok / error / 空=全部
	Page     int
	PageSize int
}

// Offset 分页偏移。
func (f AILogFilter) Offset() int {
	if f.Page < 1 {
		return 0
	}
	return (f.Page - 1) * f.PageSize
}

// AILogKindStat 按调用类型聚合的用量。
type AILogKindStat struct {
	Kind        string `json:"kind"`
	Calls       int    `json:"calls"`
	TotalTokens int64  `json:"total_tokens"`
}

// AILogMenuStat 按归属菜单聚合的用量（审计页总览用）。
type AILogMenuStat struct {
	Menu        string `json:"menu"`
	Calls       int    `json:"calls"`
	TotalTokens int64  `json:"total_tokens"`
}

// AILogSummary AI 调用记录的全量汇总（不受分页影响）。
type AILogSummary struct {
	Calls            int             `json:"calls"`
	OK               int             `json:"ok"`
	Failed           int             `json:"failed"`
	PromptTokens     int64           `json:"prompt_tokens"`
	CompletionTokens int64           `json:"completion_tokens"`
	TotalTokens      int64           `json:"total_tokens"`
	EstimatedCalls   int             `json:"estimated_calls"` // 其中估算而来（非接口给值）的条数
	AvgElapsedMs     int64           `json:"avg_elapsed_ms"`
	FirstAt          string          `json:"first_at"`
	LastAt           string          `json:"last_at"`
	ByKind           []AILogKindStat `json:"by_kind"`
	ByMenu           []AILogMenuStat `json:"by_menu"`
}
