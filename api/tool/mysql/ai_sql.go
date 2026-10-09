// ai_sql.go 自然语言 → SQL。上下文取自语义目录，不直接读库。
// 生成范围跟会话模式走：只读模式只给查询，可写模式才给写语句（执行侧另有门禁）。
// 相对时间（今天 / 上周…）以提示词里注入的当前时间为基准换算成具体值；字段的时间类型标记
// 在生成语义目录时已随目录写入上下文，换算写法据此自适应（日期字面量 / Unix 时间戳）；
// int 时间戳列按目录里探测出的单位（秒 / 毫秒）换算。
package mysql

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// 提示词按会话的读写模式分两套：生成范围必须与门禁的判断 1（会话模式）一致，
// 否则在只读模式下 AI 仍会先吐出一份写语句（再由守卫拦下），白跑一趟模型。
const sqlPromptHead = `你是 MySQL 查询助手，负责把用户的自然语言需求翻译成可执行的 SQL。
当前时间：%s
规则：
1. %s
2. 只输出 JSON，不要输出任何解释文字，不要用 markdown 代码块包裹；
3. 表名必须写成 ` + "`库名`.`表名`" + ` 的反引号形式；
4. 只用下面给出的表和字段，不要编造不存在的表或字段；
5. 每条语句末尾都写分号；查询需要限制返回量时加 LIMIT；
6. 用户提到「今天 / 昨天 / 本周 / 上周 / 最近 N 天 / 本月」这类相对时间时，按上面的当前时间换算成具体值写进 SQL，不要用 CURDATE()、NOW() 这类函数；同一天用「>= 当天 00:00:00 且 < 次日 00:00:00」表达；
7. 时间条件的写法跟字段的类型标记走：[date] / [datetime] / [timestamp] 等日期时间类型用字符串字面量（如 '2026-10-09 00:00:00'）；[int 时间戳·毫秒] 换算成毫秒级（13 位）Unix 时间戳数字，[int 时间戳·秒] 或 [int 时间戳] 换算成秒级（10 位）数字；没有标记时按字段名与说明判断存法；
8. 只写用户明确提出的条件，不要自行添加用户没提到的过滤（如 is_delete = 0、status = 1），即使是常见业务约定也不要加。
输出结构：{"sql":"查询语句","tables":["用到的表名"],"explain":"一句话说明这条 SQL 做了什么"}`

const (
	sqlReadRule  = `当前连接是只读模式：只生成只读查询（SELECT / SHOW / EXPLAIN），绝不生成 INSERT、UPDATE、DELETE、REPLACE、CREATE、ALTER、DROP、TRUNCATE 等写操作；`
	sqlWriteRule = `当前连接是可写模式：查询与写操作都能生成；写操作必须带明确的限定条件（WHERE 或等价的过滤），不要输出无条件的整表 UPDATE / DELETE；`
)

// sqlSystemPrompt 取当前模式对应的提示词，并注入当前时间（相对时间换算的基准）。
func sqlSystemPrompt(mode string) string {
	rule := sqlReadRule
	if mode == ModeWrite {
		rule = sqlWriteRule
	}
	return fmt.Sprintf(sqlPromptHead, nowText(), rule)
}

// nowText 当前时间的中文表达（带星期）：「今天 / 本周」这类相对时间的换算基准。
func nowText() string {
	weekdays := [...]string{"日", "一", "二", "三", "四", "五", "六"}
	now := time.Now()
	return now.Format("2006-01-02 15:04:05") + " 星期" + weekdays[int(now.Weekday())]
}

const sqlUserPrompt = `数据库：%s

可用表（含用途与字段说明；时间相关字段带 [类型] 标记）：

%s

用户需求：%s`

// sqlReply 模型返回的生成结果。
type sqlReply struct {
	SQL     string   `json:"sql"`
	Tables  []string `json:"tables"`
	Explain string   `json:"explain"`
}

type aiSQLReq struct {
	Schema   string `json:"schema" binding:"required"`
	Question string `json:"question" binding:"required"`
}

// GenerateSQL POST /api/mysql/:id/ai/sql
func (h *Handler) GenerateSQL(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req aiSQLReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	question := strings.TrimSpace(req.Question)
	if question == "" {
		resp.Fail(c, resp.CodeBadRequest, "请先描述你想查什么")
		return
	}
	schema := strings.TrimSpace(req.Schema)
	if schema == "" {
		resp.Fail(c, resp.CodeBadRequest, "请先选择数据库")
		return
	}
	client, aiCfg, err := h.aiClient()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}

	// 目录范围 = 当前库优先 + 连接内其余库补足：生成的 SQL 一律用 `库名`.`表名` 限定，
	// 跨库查询由这里直接支撑（表数超限时按相关度挑 sqlContextTables 张，上下文不会爆）。
	current, err := h.aiRepo.ListCatalog(conn.ID, schema)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "读取语义目录失败")
		return
	}
	others, err := h.aiRepo.ListCatalogAll(conn.ID)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "读取语义目录失败")
		return
	}
	entries := mergeCatalogEntries(current, others, schema)
	if len(entries) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "还没有任何语义目录，请先在 AI 面板点「生成」或「生成全库目录」")
		return
	}

	picked := pickTables(entries, question, sqlContextTables)
	// 生成范围跟着会话模式走：与门禁的判断 1 同源，两处不能有两套口径
	mode := h.modes.get(conn.ID, defaultMode(conn))
	// 每次生成都落一条调用记录（含 token 用量）；成功时把记录 id 一并回传前端，
	// 追加进编辑器执行时带回来，两张记录表由此绑定。
	trace := h.beginTrace(aiTraceParams{
		Kind:       model.AILogKindSQL,
		Conn:       conn,
		Schema:     schema,
		Mode:       mode,
		Model:      aiCfg.Model,
		BaseURL:    aiCfg.BaseURL,
		TableCount: len(picked),
		Question:   question,
	})
	defer trace.done()

	ctx := c.Request.Context()
	res, err := client.Chat(ctx, []aiopenai.Message{
		{Role: "system", Content: sqlSystemPrompt(mode)},
		{Role: "user", Content: fmt.Sprintf(sqlUserPrompt, schema, catalogContext(picked), question)},
	})
	if err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	trace.fill(res)
	text := res.Content

	var reply sqlReply
	if err := unmarshalModelJSON(text, &reply); err != nil {
		trace.fail(fmt.Errorf("解析模型返回失败: %w", err))
		resp.Fail(c, resp.CodeInternal, "解析模型返回失败: "+err.Error())
		return
	}
	trace.row.Content = truncateRunes(text, logContentMax)
	sqlText := strings.TrimSpace(reply.SQL)
	if sqlText == "" {
		trace.fail(fmt.Errorf("模型返回的内容里没有 SQL"))
		resp.Fail(c, resp.CodeInternal, "模型没有生成 SQL，请换个说法再试")
		return
	}
	// 每次生成都以分号收尾：前端会把结果追加进编辑器，多条语句拼在一起时每条都要自带终结符
	sqlText = ensureTrailingSemicolon(sqlText)
	if err := guardGeneration(sqlText, mode); err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	trace.row.Expr = truncateRunes(sqlText, logSQLMax)

	// 静态分类随结果下发：前端据此标注「查询 / 数据变更 / 结构变更」，
	// 并提示追加入编辑器后执行时会走哪一档确认。
	_, an := analyzeSQL(sqlText)
	tables := reply.Tables
	if tables == nil {
		tables = []string{}
	}
	resp.OK(c, model.MySQLAISQLResult{
		SQL:            sqlText,
		Tables:         tables,
		Explain:        strings.TrimSpace(reply.Explain),
		UsedCatalog:    len(picked),
		SQLType:        an.Type,
		StatementCount: len(an.Statements),
		ConfirmLevel:   confirmLevel(mode, an.Type),
		Mode:           mode,
		Usage:          trace.usage(1),
	})
}

// ensureTrailingSemicolon 统一以分号收尾（模型漏写时兜底补上，多写的只保留一个）：
// 生成结果会被前端追加进编辑器，多条语句拼在一起时每条都必须自带终结符。
func ensureTrailingSemicolon(s string) string {
	return strings.TrimRight(s, "; \t\r\n") + ";"
}

// guardGeneration 生成侧守卫：按当前会话模式决定放行的范围。
//
// 只读模式：只放行「单条 DQL」。必须按「多语句切分 + 取风险最高一条」判定，
// 只看首关键词时 `SELECT 1; DROP TABLE t` 会被 SELECT 蒙混过关。
// 可写模式：写语句与多语句都放行（真正确认在执行侧的两段式门禁）。
//
// 两种模式都要挡掉「看着像 SELECT、实际会加锁或往数据库服务器落盘」的写法：
// 这类语句在门禁里被判成 DQL，执行时不会触发确认弹窗。
func guardGeneration(sqlText, mode string) error {
	stmts, an := analyzeSQL(sqlText)
	if len(stmts) == 0 {
		return fmt.Errorf("模型没有生成可执行的语句，请换个说法再试")
	}
	if mode != ModeWrite && (len(stmts) > 1 || an.Type != sqlDQL) {
		return fmt.Errorf("当前是只读模式，AI 只生成查询语句，而模型给出的不是单条只读查询"+
			"（%s · %s，共 %d 条语句），已拒绝返回；需要生成写操作请先切到可写模式",
			an.Kind, an.Type, len(stmts))
	}
	plain := strings.ToUpper(blockCommentRe.ReplaceAllString(sqlText, " "))
	for _, bad := range []string{"FOR UPDATE", "FOR SHARE", "LOCK IN SHARE MODE", "INTO OUTFILE", "INTO DUMPFILE"} {
		if strings.Contains(plain, bad) {
			return fmt.Errorf("模型生成的语句含 %s，会加锁或往数据库服务器落盘，已拒绝返回", bad)
		}
	}
	return nil
}

// mergeCatalogEntries 合并生成上下文：当前库的条目在前（同分时稳定排序会保住这个次序），
// 其余库的条目按库名、表名有序地排在后面；当前库条目不重复入选。
func mergeCatalogEntries(current, all []model.MySQLAICatalogEntry, schema string) []model.MySQLAICatalogEntry {
	out := make([]model.MySQLAICatalogEntry, 0, len(all))
	out = append(out, current...)
	inCurrent := map[string]bool{}
	for _, e := range current {
		inCurrent[e.TableName] = true
	}
	for _, e := range all {
		if e.SchemaName != schema || !inCurrent[e.TableName] {
			out = append(out, e)
		}
	}
	return out
}

// pickTables 挑出与问题最相关的表：表名直接命中问题的置顶，
// 其余按「用途 + 字段说明 + 字段名」与问题用词的重合度补足。
func pickTables(entries []model.MySQLAICatalogEntry, question string, max int) []model.MySQLAICatalogEntry {
	if len(entries) <= max {
		return entries
	}
	kws := keywords(question)
	q := strings.ToLower(question)

	type scored struct {
		e     model.MySQLAICatalogEntry
		score int
	}
	list := make([]scored, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(e.TableName)
		score := 0
		if strings.Contains(q, name) {
			score += 1000
		} else if base := strings.TrimPrefix(name, "t_"); base != name && len(base) >= 2 && strings.Contains(q, base) {
			score += 500
		}
		hay := name + " " + strings.ToLower(e.Purpose)
		for _, c := range e.Columns {
			hay += " " + strings.ToLower(c.Name) + " " + strings.ToLower(c.Note)
		}
		for _, k := range kws {
			if strings.Contains(hay, k) {
				score += len([]rune(k))
			}
		}
		list = append(list, scored{e: e, score: score})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })

	out := make([]model.MySQLAICatalogEntry, 0, max)
	for i := 0; i < max && i < len(list); i++ {
		out = append(out, list[i].e)
	}
	return out
}

// keywords 切出用于匹配的词：连续英文/数字算整词，中文取 2-gram。
func keywords(q string) []string {
	out := []string{}
	seen := map[string]bool{}
	var ascii, cjk []rune

	add := func(w string) {
		if w == "" || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	flushASCII := func() {
		if len(ascii) >= 2 {
			add(strings.ToLower(string(ascii)))
		}
		ascii = nil
	}
	flushCJK := func() {
		for i := 0; i+2 <= len(cjk); i++ {
			add(string(cjk[i : i+2]))
		}
		cjk = nil
	}
	for _, r := range q {
		switch {
		case r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			flushCJK()
			ascii = append(ascii, r)
		case r >= 0x4E00 && r <= 0x9FFF:
			flushASCII()
			cjk = append(cjk, r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return out
}

// catalogContext 把选中的表拼成紧凑上下文（表用途 + 字段名 + 含义）。
// 类型只为时间相关字段渲染 [类型] 标记：生成 SQL 只需在时间换算上区分存法，
// 其余列的类型不上上下文（省 token）。
func catalogContext(entries []model.MySQLAICatalogEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString("表 `")
		b.WriteString(e.SchemaName)
		b.WriteString("`.`")
		b.WriteString(e.TableName)
		b.WriteString("`")
		if e.Purpose != "" {
			b.WriteString("（")
			b.WriteString(e.Purpose)
			b.WriteString("）")
		}
		b.WriteByte('\n')
		for _, c := range e.Columns {
			b.WriteString("  ")
			b.WriteString(c.Name)
			if hint := columnTypeHint(c); hint != "" {
				b.WriteByte(' ')
				b.WriteString(hint)
			}
			if c.Note != "" {
				b.WriteString("  ")
				b.WriteString(c.Note)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// columnTypeHint 时间相关字段的类型标记：日期时间类型原样标（datetime / date / timestamp…），
// 整数类型结合名字与说明判断是否 Unix 时间戳，带探测单位就标出秒 / 毫秒。其余列返回空串；
// 旧目录没有类型标记也不标。
func columnTypeHint(c model.MySQLAIColumn) string {
	t := strings.ToLower(strings.TrimSpace(c.Type))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i] // int(11) -> int
	}
	if i := strings.IndexByte(t, ' '); i >= 0 {
		t = t[:i] // bigint unsigned -> bigint
	}
	switch t {
	case "date", "datetime", "timestamp", "time", "year":
		return "[" + t + "]"
	}
	if intFamily(t) != "" && looksTemporalColumn(c.Name, c.Note) {
		switch c.Unit {
		case "ms":
			return "[int 时间戳·毫秒]"
		case "s":
			return "[int 时间戳·秒]"
		}
		return "[int 时间戳]"
	}
	return ""
}

// intFamily 归一化整数列类型（去掉括号精度与 unsigned 等后缀），非整型返回空串：
// 建目录时的单位探测与生成 SQL 时的类型标记共用这一份判定。
func intFamily(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, ' '); i >= 0 {
		t = t[:i]
	}
	switch t {
	case "int", "bigint", "smallint", "mediumint", "tinyint":
		return t
	}
	return ""
}

// looksTemporalColumn 名字或说明带时间语义的整数列（Unix 时间戳的常见命名 / 注释）。
func looksTemporalColumn(name, note string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(n, "time") || strings.HasSuffix(n, "_at") || strings.HasSuffix(n, "_date") ||
		n == "date" || n == "ts" || strings.HasSuffix(n, "_ts") {
		return true
	}
	return strings.Contains(note, "时间") || strings.Contains(note, "日期") || strings.Contains(note, "戳")
}
