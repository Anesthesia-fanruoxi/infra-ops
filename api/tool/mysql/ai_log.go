// ai_log.go 使用记录：AI 调用记录的落库装配（写统一表 ai_logs，menu=mysql）
// 与 SQL 执行记录（mysql_sql_logs）的装配、查询与清空。
//
// 记的是旁路信息：任何写库失败都不改变 AI 调用或 SQL 执行本身的结果，故落库错误一律吞掉。
// AI 记录的查询 / 清空走统一入口（api/ailog，/api/ai/logs?menu=mysql）。
package mysql

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/setting"
)

// 记录里文本字段的截断长度：留够排错用的信息，又不让单条记录膨胀到几 MB。
const (
	logSQLMax      = 4000
	logContentMax  = 4000
	logQuestionMax = 1000
	logErrMax      = 600

	defaultLogPage = 20
	maxLogPage     = 200
)

// aiTrace 一次模型调用的记录装配。
//
// 三个写入时机：fill 记用量、fail 标失败原因、done 落库。
// done 幂等（落过就不再落），调用方可以放心用 defer 兜底。
type aiTrace struct {
	h      *Handler
	row    model.AILog
	start  time.Time
	status string
	reason string
	saved  bool
}

// aiTraceParams 开始一次调用记录所需的上下文。
type aiTraceParams struct {
	Kind       string
	Conn       *model.MySQLConn
	Schema     string
	Mode       string
	Model      string
	BaseURL    string
	BatchNo    int
	BatchTotal int
	TableCount int
	Question   string
}

func (h *Handler) beginTrace(p aiTraceParams) *aiTrace {
	row := model.AILog{
		Menu:       model.AIMenuMySQL,
		Kind:       p.Kind,
		SchemaName: p.Schema,
		Mode:       p.Mode,
		Model:      p.Model,
		BaseURL:    p.BaseURL,
		BatchNo:    p.BatchNo,
		BatchTotal: p.BatchTotal,
		TableCount: p.TableCount,
		Question:   truncateRunes(p.Question, logQuestionMax),
		Status:     model.LogStatusOK,
	}
	if p.Conn != nil {
		row.ConnID = p.Conn.ID
		row.ConnName = p.Conn.Name
	}
	return &aiTrace{h: h, start: time.Now(), row: row, status: model.LogStatusOK}
}

// fill 记下接口返回的用量；服务端回显了模型名则一并覆盖（多模型网关下更准）。
func (t *aiTrace) fill(res *aiopenai.Result) {
	if res == nil {
		return
	}
	if res.Model != "" {
		t.row.Model = res.Model
	}
	t.row.PromptTokens = res.Usage.PromptTokens
	t.row.CompletionTokens = res.Usage.CompletionTokens
	t.row.TotalTokens = res.Usage.TotalTokens
	if res.Usage.FromAPI {
		t.row.TokenSource = model.TokenSourceAPI
	} else {
		t.row.TokenSource = model.TokenSourceEstimated
	}
}

// fail 标注这次调用最终没成功：模型报错、返回无法解析、生成结果被守卫拦下都算。
func (t *aiTrace) fail(err error) {
	if err == nil {
		return
	}
	t.status = model.LogStatusError
	if t.reason == "" {
		t.reason = err.Error()
	}
}

// done 落库；重复调用只落一次。
func (t *aiTrace) done() {
	if t.saved {
		return
	}
	t.saved = true
	if t.h.aiLogRepo == nil { // 未装配统一记录仓储时直接跳过，不影响调用结果
		return
	}
	t.row.ElapsedMs = time.Since(t.start).Milliseconds()
	t.row.Status = t.status
	t.row.Error = truncateRunes(t.reason, logErrMax)
	_, _ = t.h.aiLogRepo.Add(&t.row)
}

// usage 把本次用量整理成下发给前端的结构；calls 为本轮实际发起的模型调用次数。
func (t *aiTrace) usage(calls int) model.MySQLAIUsage {
	return model.MySQLAIUsage{
		Calls:            calls,
		PromptTokens:     int64(t.row.PromptTokens),
		CompletionTokens: int64(t.row.CompletionTokens),
		TotalTokens:      int64(t.row.TotalTokens),
		Source:           t.row.TokenSource,
		ElapsedMs:        time.Since(t.start).Milliseconds(),
	}
}

// recordSQLLog 落一条 SQL 执行记录。
//
// 只记真正走到执行的语句（连不上库、超时、执行报错都算一次尝试）；只回 need_confirm
// 的那一步还没执行，不记。elapsed 是这次操作的总耗时（含建连），不只是语句本身的耗时。
func (h *Handler) recordSQLLog(conn *model.MySQLConn, req queryReq, an sqlAnalysis, mode string,
	res *model.MySQLQueryResult, execErr error, elapsed time.Duration) {
	if h.logRepo == nil {
		return
	}
	row := model.MySQLSQLLog{
		ConnID:         conn.ID,
		ConnName:       conn.Name,
		SchemaName:     strings.TrimSpace(req.Schema),
		SQL:            truncateRunes(req.SQL, logSQLMax),
		StatementCount: len(an.Statements),
		SQLType:        an.Type,
		StatementKind:  an.Kind,
		Mode:           mode,
		Confirmed:      req.Confirmed,
		ElapsedMs:      elapsed.Milliseconds(),
		Status:         model.LogStatusOK,
	}
	if res != nil {
		row.RowCount = res.RowCount
		row.AffectedRows = res.AffectedRows
	}
	if execErr != nil {
		row.Status = model.LogStatusError
		row.Error = truncateRunes(execErr.Error(), logErrMax)
	}
	_, _ = h.logRepo.AddSQLLog(&row)
}

func truncateRunes(s string, n int) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// logRetentionDays 记录保留天数，与部署历史共用同一个设置（默认 30 天）。
func (h *Handler) logRetentionDays() int {
	if h.settings == nil {
		return 0
	}
	v, _ := h.settings.Get(setting.SettingLogRetentionDays)
	days, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || days <= 0 {
		return 0
	}
	return days
}

// parseLogFilter 解析 SQL 执行记录的查询条件。
func parseLogFilter(c *gin.Context) model.MySQLLogFilter {
	connID, _ := strconv.ParseInt(c.Query("conn_id"), 10, 64)
	page, _ := strconv.Atoi(c.Query("page"))
	size, _ := strconv.Atoi(c.Query("page_size"))
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = defaultLogPage
	}
	if size > maxLogPage {
		size = maxLogPage
	}
	return model.MySQLLogFilter{
		ConnID:   connID,
		Status:   strings.TrimSpace(c.Query("status")),
		SQLType:  strings.TrimSpace(c.Query("sql_type")),
		Page:     page,
		PageSize: size,
	}
}

// GetSQLLogs GET /api/mysql/sql-logs
func (h *Handler) GetSQLLogs(c *gin.Context) {
	f := parseLogFilter(c)
	list, total, err := h.logRepo.ListSQLLogs(f)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "读取 SQL 执行记录失败")
		return
	}
	sum, err := h.logRepo.SummarizeSQLLogs(f)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "汇总 SQL 执行记录失败")
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "summary": sum, "retention_days": h.logRetentionDays()})
}

// ClearSQLLogs DELETE /api/mysql/sql-logs?conn_id=
func (h *Handler) ClearSQLLogs(c *gin.Context) {
	connID, _ := strconv.ParseInt(c.Query("conn_id"), 10, 64)
	n, err := h.logRepo.ClearLogs(connID)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "清空 SQL 执行记录失败")
		return
	}
	resp.OK(c, gin.H{"deleted": n})
}

// accumulateUsage 把一次模型调用的用量并进累计值（语义目录逐批请求，故要相加）。
// 只要有一批的用量是估算的，整体就按估算标注 —— 宁可标低可信度，也不假称精确。
func accumulateUsage(dst *model.MySQLAIUsage, res *aiopenai.Result) {
	if res == nil {
		return
	}
	dst.Calls++
	dst.PromptTokens += int64(res.Usage.PromptTokens)
	dst.CompletionTokens += int64(res.Usage.CompletionTokens)
	dst.TotalTokens += int64(res.Usage.TotalTokens)
	if !res.Usage.FromAPI {
		dst.Source = model.TokenSourceEstimated
	} else if dst.Source == "" {
		dst.Source = model.TokenSourceAPI
	}
}
