// ai_explain.go 执行计划分析：只分析 EXPLAIN 语句的执行计划结果。
//
// 安全边界（本功能的核心约束）：数据库里的业务行数据绝不能离开本机送去 AI。
// 为此在协议层就不给「上传结果数据」留任何口子——
//
//  1. 分析接口只收 SQL 原文与库名，不收任何查询结果；前端回传的快照一律不信；
//  2. SQL 必须是单条 EXPLAIN 执行计划（explainCanAnalyze 判定），不是的直接拒绝：
//     「当前语句不是 EXPLAIN，无法分析」；
//  3. 通过校验后服务端重跑一遍 EXPLAIN，并把执行计划与相关表的字段 / 索引 /
//     行数大小统计（information_schema 元信息）组装成分析上下文送模型——
//     全是结构元信息，表数据明细绝不出本机。
//
// 判定口径（explainTarget）：EXPLAIN [ANALYZE|EXTENDED|PARTITIONS|FORMAT=fmt] 之后
// 必须跟可解释语句（SELECT / UPDATE / DELETE / INSERT / REPLACE / WITH）。
// EXPLAIN 表名（等价 DESC 的表结构）、EXPLAIN FOR CONNECTION（无法重跑复现）
// 与多语句混合都不算执行计划，不给分析。
package mysql

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// 计划摘要上限：EXPLAIN 计划一般几行到几十行（TREE 格式较宽），截断只为兜底；
// 单值出奇长时截掉，正常计划都能整份送进模型。
const (
	explainDigestMaxRows = 60
	explainCellMax       = 160
	explainMaxIndexDDL   = 3 // 分析返回的建索引语句条数上限（prompt 约定最多 3 条，服务端再兜一层）
)

// explainBacktickRe 反引号标识符：判定 EXPLAIN 目标前先剥掉，
// 避免表名恰好叫关键字（如 `select`）时把表结构误判成执行计划。
var explainBacktickRe = regexp.MustCompile("`[^`]*`")

// explainTarget 判定 EXPLAIN 之后是否跟着「可执行计划的目标语句」。
// 修饰词按词宽松跳过（词切分后 FORMAT=JSON 是 FORMAT、JSON 两个词）；
// 第一个有效关键词决定结论：可解释语句 → true，其余（表名、FOR CONNECTION…）→ false。
func explainTarget(stmt string) bool {
	s := blockCommentRe.ReplaceAllString(stmt, " ")
	s = explainBacktickRe.ReplaceAllString(s, " ")
	words := wordRe.FindAllString(s, -1)
	if len(words) == 0 || !strings.EqualFold(words[0], "EXPLAIN") {
		return false
	}
	for _, w := range words[1:] {
		switch strings.ToUpper(w) {
		case "ANALYZE", "EXTENDED", "PARTITIONS", "FORMAT", "JSON", "TRADITIONAL", "TREE":
			continue
		case "SELECT", "UPDATE", "DELETE", "INSERT", "REPLACE", "WITH":
			return true
		default:
			return false
		}
	}
	return false
}

// explainCanAnalyze 单条输入是否是可分析的执行计划语句（执行入口标记与分析接口共用）。
// 多语句里混 EXPLAIN 也不放开：分析对象必须是唯一一条。
func explainCanAnalyze(stmts []string, an sqlAnalysis) bool {
	return len(stmts) == 1 && an.Kind == "EXPLAIN" && explainTarget(stmts[0])
}

// ---------------- 提示词 ----------------

const explainPromptHead = `你是 MySQL 执行计划分析助手，负责结合执行计划与相关表结构，帮助定位查询的性能问题。
规则：
1. 只输出 JSON（键：summary 字符串、findings 字符串数组、suggestions 字符串数组、index_statements 字符串数组、optimized_sql 字符串），不要输出任何解释文字，不要用 markdown 代码块包裹；
2. 结论必须基于给出的执行计划与表结构统计，不要编造上下文里没有的信息；
3. summary 给总体结论（2-3 句）：这条查询大致怎么执行，走了哪个索引（或为何没走），有没有明显的性能隐患；
4. findings 对照计划与表的真实索引定义逐条判定：全表扫描 type=ALL、key 为空但 possible_keys 里有可用索引、命中索引只吃到最左前缀、条件列被函数 / 隐式类型转换挡住索引、扫描行数 rows 相对表估算行数偏大、Using temporary / Using filesort、联接顺序与驱动表选择等，每条一句话；
5. suggestions 给可操作的优化建议：补索引要指明具体列与列序（对照现有索引说明为何不适用），SQL 改写要指明怎么让条件吃上索引，每条一句话；
6. 凡是建议补索引 / 调整索引，把对应建索引语句放进 index_statements：形如 ALTER TABLE ` + "`库名`.`表名`" + ` ADD INDEX ` + "`idx_名称`" + ` (` + "`列1`, `列2`" + `)，每条一行、以分号结尾、完整可执行，最多 3 条；不涉及索引或没把握就给空数组；
7. optimized_sql 给优化后的语句：只有原语句存在改动小、语义完全不变的空间时才给（如索引列被函数 / 隐式类型转换包住时改写为等价条件、时间条件区间化、条件书写顺序按现有复合索引列序整理），必须保留原 EXPLAIN 前缀、完整可执行；只允许局部小改——不得改变语义、表、列、联接关系、聚合与结果集，禁止整体重写；没有这类空间或没把握就给空字符串；
8. 表行数与大小是 information_schema 的估算值，只作量级参考；上下文不含业务数据，不要臆造数据内容；
9. 全部用中文，简洁直接，不说空话。`

const explainUserPrompt = `被分析的语句：%s
执行计划行数：%d（最多列出 %d 行）

执行计划：
%s

相关表结构与统计（information_schema 元信息，仅结构、无业务数据）：
%s`

// explainReply 模型返回的解读结果。
type explainReply struct {
	Summary         string   `json:"summary"`
	Findings        []string `json:"findings"`
	Suggestions     []string `json:"suggestions"`
	IndexStatements []string `json:"index_statements"`
	OptimizedSQL    string   `json:"optimized_sql"`
}

// explainIndexList 清洗模型给出的建索引语句：去空白、丢空项、封顶条数；
// 返回非 nil 空切片（JSON 输出 []，前端无需判空）。仅回传展示供复制，服务端不会自动执行。
func explainIndexList(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
		if len(out) >= explainMaxIndexDDL {
			break
		}
	}
	return out
}

// explainSameSQL 忽略空白 / 大小写 / 末尾分号后判断两条语句是否等价——
// 模型把「优化后语句」原样抄回时视为没有改动，前端不展示该块。
func explainSameSQL(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, ";") {
			s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
		}
		return strings.ToLower(strings.Join(strings.Fields(s), " "))
	}
	return norm(a) == norm(b)
}

type aiExplainReq struct {
	SQL    string `json:"sql" binding:"required"`
	Schema string `json:"schema"`
}

// AnalyzeExplain POST /api/mysql/:id/query/analyze
//
// 边界三层：执行入口标记（Query 响应的 can_analyze）→ 本接口重校验（非 EXPLAIN
// 直接拒绝，不信任前端）→ 重跑取计划（接口不接收任何查询结果，只送计划文本）。
func (h *Handler) AnalyzeExplain(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req aiExplainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	// 后端硬拦截：不是单条 EXPLAIN 执行计划就直接拒绝——从源头保证业务数据不会
	// 因为误传结果而流向 AI（本接口压根没有接收结果数据的通道）。
	stmts, an := analyzeSQL(req.SQL)
	if !explainCanAnalyze(stmts, an) {
		resp.Fail(c, resp.CodeBadRequest, "当前语句不是 EXPLAIN，无法分析（仅支持单条 EXPLAIN SELECT/UPDATE/DELETE/INSERT/REPLACE 这类执行计划语句；EXPLAIN 表名得到的是表结构，不支持分析）")
		return
	}
	client, aiCfg, err := h.aiClient()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}

	// 重跑 EXPLAIN 取当前执行计划（与 metrics 的解读同口径：不信前端快照），
	// 随后在同一个查询超时内补取相关表结构与统计（information_schema 元信息）；
	// 两者都只有结构与统计，不含业务行数据；AI 调用不套查询超时（按 AI 配置，默认 90s）。
	qctx, qcancel := context.WithTimeout(c.Request.Context(), queryTimeout(0))
	sess, err := h.openForSchema(conn, strings.TrimSpace(req.Schema))
	if err != nil {
		qcancel()
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer sess.Close()
	res, err := execStatements(qctx, sess.db, stmts, an, 0)
	timedOut := qctx.Err() == context.DeadlineExceeded
	if err != nil {
		qcancel()
		if timedOut {
			resp.Fail(c, resp.CodeInternal, "重跑 EXPLAIN 超时，已中断")
			return
		}
		resp.Fail(c, resp.CodeInternal, "重跑 EXPLAIN 失败: "+err.Error())
		return
	}
	if len(res.Rows) == 0 {
		qcancel()
		resp.Fail(c, resp.CodeInternal, "EXPLAIN 没有返回执行计划，无法分析")
		return
	}
	digest := buildExplainDigest(res)
	// 相关表结构与统计：让模型能对照真实索引判断是否走索引；
	// 只取 information_schema 元信息，与计划同属结构数据，绝无业务行数据。
	digest.Tables = explainTablesContext(qctx, sess.db, res, stmts[0], strings.TrimSpace(req.Schema))
	qcancel()

	mode := h.modes.get(conn.ID, defaultMode(conn))
	trace := h.beginTrace(aiTraceParams{
		Kind:    model.AILogKindExplain,
		Conn:    conn,
		Schema:  strings.TrimSpace(req.Schema),
		Mode:    mode,
		Model:   aiCfg.Model,
		BaseURL: aiCfg.BaseURL,
	})
	defer trace.done()
	trace.row.Expr = truncateRunes(strings.TrimSpace(req.SQL), logSQLMax)

	aiRes, err := client.Chat(c.Request.Context(), []aiopenai.Message{
		{Role: "system", Content: explainPromptHead},
		{Role: "user", Content: fmt.Sprintf(explainUserPrompt, strings.TrimSpace(req.SQL), digest.RowCount, explainDigestMaxRows, strings.Join(digest.Lines, "\n"), explainTablesText(digest.Tables))},
	})
	if err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	trace.fill(aiRes)
	trace.row.Content = truncateRunes(aiRes.Content, logContentMax)

	var reply explainReply
	if err := unmarshalModelJSON(aiRes.Content, &reply); err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeInternal, "解析模型返回失败: "+err.Error())
		return
	}
	findings, suggestions := reply.Findings, reply.Suggestions
	if findings == nil {
		findings = []string{}
	}
	if suggestions == nil {
		suggestions = []string{}
	}
	optSQL := strings.TrimSpace(reply.OptimizedSQL)
	if explainSameSQL(optSQL, stmts[0]) {
		optSQL = "" // 模型把原句抄回：没有实际改动，不展示
	}
	resp.OK(c, model.MySQLAIExplainResult{
		Summary:         strings.TrimSpace(reply.Summary),
		Findings:        findings,
		Suggestions:     suggestions,
		IndexStatements: explainIndexList(reply.IndexStatements),
		OptimizedSQL:    optSQL,
		Digest:          digest,
		Usage:           trace.usage(1),
	})
}

// buildExplainDigest 把计划结果整理成文本：每行一个计划节点，「列名=值」空格拼接；
// 行多时只取前若干行并标记截断。输入只有计划行，不含业务数据。
func buildExplainDigest(res *model.MySQLQueryResult) model.MySQLExplainDigest {
	d := model.MySQLExplainDigest{RowCount: len(res.Rows), Lines: make([]string, 0)}
	for i, row := range res.Rows {
		if i >= explainDigestMaxRows {
			d.Truncated = true
			break
		}
		parts := make([]string, 0, len(res.Columns))
		for ci, col := range res.Columns {
			v := "NULL"
			if ci < len(row) {
				v = truncateRunes(explainValue(row[ci]), explainCellMax)
			}
			parts = append(parts, col.Name+"="+v)
		}
		d.Lines = append(d.Lines, strings.Join(parts, " "))
	}
	return d
}

// explainValue 计划单元格转文本：NULL 显式标注；时间列按常见格式；其余 fmt。
func explainValue(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprintf("%v", x)
	}
}
