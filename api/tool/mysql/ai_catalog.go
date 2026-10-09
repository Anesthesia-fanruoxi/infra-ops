// ai_catalog.go 表语义目录：按库读表与字段注释 → 分批交给模型归纳用途与字段含义 → 落库缓存。
// token 约法：输入用紧凑 JSON（缩进只给眼睛看，进模型就是白花），输出用 notes 顺序数组
// （字段名输入里已有，不复述）+ 长度双限（purpose 30 字 / note 20 字）+「注释清楚就照抄」。
// 类型标记：目录同时记下每个字段的真实列类型，生成 SQL 时据此把相对时间自适应成
// 日期字面量或 Unix 时间戳；int 时间戳列落库前抽一条真实值探测单位（秒 / 毫秒）一并记下。
package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
)

const (
	// 单批送进模型的表数量与字符上限：批太小请求次数多，批太大容易超上下文
	catalogBatchTables = 15
	catalogBatchChars  = 14000
	// 目录生成是逐批串行的模型调用，给足整体超时
	catalogTotalTimeout = 10 * time.Minute
)

// tableBrief 送进模型的表结构摘要（只保留语义相关字段，省 token）。
type tableBrief struct {
	Table   string        `json:"table"`
	Comment string        `json:"comment,omitempty"`
	Columns []columnBrief `json:"columns"`
}

type columnBrief struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Key     string `json:"key,omitempty"`
	Comment string `json:"comment,omitempty"`
	// Unit 时间戳单位（ms / s）：落库前由真实值探测填入，只在本地流转，不进模型请求
	Unit string `json:"-"`
}

// catalogReply 模型返回的单表归纳结果。
// notes 与输入 columns 顺序一一对应：省去逐字段复述 name 的输出 token，
// 按顺序对齐后由 buildCatalogEntry 回填到真实字段。
type catalogReply struct {
	Table   string   `json:"table"`
	Purpose string   `json:"purpose"`
	Notes   []string `json:"notes"`
}

// catalogSystemPrompt 目标：用尽量少的输出 token 拿到每表用途与每字段含义。
// notes 数组替掉 {"name":..,"note":..} 对象（字段名在输入里已有，不必复述）；
// 长度双限（30 字 / 20 字）与「照抄不扩写」把 completion token 压住。
const catalogSystemPrompt = `你是数据库元数据整理助手，把 MySQL 表结构与注释整理成「语义目录」，供后续自然语言生成 SQL 时作上下文。规则：
1. 只输出 JSON，不要解释文字，不要用 markdown 代码块包裹；
2. 输出数组与输入表一一对应：table 与输入完全一致，不增不漏；
3. purpose 一句话说明这张表存什么、什么场景会查，不超过 30 字；
4. notes 是字段说明数组：数量必须与输入 columns 完全相等、顺序一一对应，每个说明不超过 20 字；
5. 字段注释已能说明含义时直接照抄，不要润色扩写；注释为空时再依据字段名与类型推断；不得编造输入里没有的字段。`

const catalogUserPrompt = `数据库：%s

表结构与注释（JSON，notes 需与各表 columns 顺序一一对应）：

%s

输出结构：
[{"table":"表名","purpose":"用途","notes":["字段1说明","字段2说明"]}]`

// tableFingerprint 表结构指纹：表注释 + 每个字段的名字/类型/注释。
// 结构没变则目录可以一直复用，变了才重生成。
func tableFingerprint(t model.MySQLTable, cols []model.MySQLColumn) string {
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteByte('|')
	b.WriteString(t.Type)
	b.WriteByte('|')
	b.WriteString(t.Comment)
	for _, c := range cols {
		b.WriteByte('\n')
		b.WriteString(c.Name)
		b.WriteByte(':')
		b.WriteString(c.Type)
		b.WriteByte(':')
		b.WriteString(c.Comment)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// toBrief 把库里的表与字段裁剪成送模型的摘要。
func toBrief(t model.MySQLTable, cols []model.MySQLColumn) tableBrief {
	b := tableBrief{Table: t.Name, Comment: t.Comment, Columns: make([]columnBrief, 0, len(cols))}
	for _, c := range cols {
		b.Columns = append(b.Columns, columnBrief{Name: c.Name, Type: c.Type, Key: c.Key, Comment: c.Comment})
	}
	return b
}

// briefCost 估算一条表摘要的字符数，用于分批。
func briefCost(b tableBrief) int {
	n := len(b.Table) + len(b.Comment) + 40
	for _, c := range b.Columns {
		n += len(c.Name) + len(c.Type) + len(c.Comment) + 12
	}
	return n
}

// unmarshalModelJSON 容错解析模型返回的 JSON：
// 剥掉 markdown 围栏，并截取首个数组/对象（模型常在 JSON 前后带解释文字）。
func unmarshalModelJSON(text string, out interface{}) error {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(strings.TrimSpace(s), "json")
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return fmt.Errorf("返回内容里没有 JSON")
	}
	closer := byte(']')
	if s[start] == '{' {
		closer = '}'
	}
	end := strings.LastIndexByte(s, closer)
	if end <= start {
		return fmt.Errorf("返回的 JSON 结构不完整")
	}
	return json.Unmarshal([]byte(s[start:end+1]), out)
}

type catalogReq struct {
	Schema  string   `json:"schema"`  // 只生成这一个库
	Schemas []string `json:"schemas"` // 弹框勾选的多库批量生成（去重、系统库直接被忽略）
	All     bool     `json:"all"`     // true = 整个连接的全部非系统库逐库生成（支撑跨库查询）
	Force   bool     `json:"force"`   // 结构未变也重生成
}

// GetCatalog GET /api/mysql/:id/ai/catalog?schema=
// schema 省略时返回整个连接的目录（浏览抽屉用），并附每个库的条数。
func (h *Handler) GetCatalog(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	schema := strings.TrimSpace(c.Query("schema"))
	list, err := h.aiRepo.ListCatalog(conn.ID, schema)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "读取语义目录失败")
		return
	}
	if schema != "" {
		resp.OK(c, gin.H{"list": list, "total": len(list)})
		return
	}
	all, err := h.aiRepo.ListCatalogAll(conn.ID)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "读取语义目录失败")
		return
	}
	// 每个库的条数与用途摘要：抽屉按库折叠展示，一眼看出哪个库建了目录、覆盖多少表
	type schemaSummary struct {
		Name  string `json:"name"`
		Total int    `json:"total"`
	}
	bySchema := map[string]*schemaSummary{}
	order := []string{}
	for _, e := range all {
		s, hit := bySchema[e.SchemaName]
		if !hit {
			s = &schemaSummary{Name: e.SchemaName}
			bySchema[e.SchemaName] = s
			order = append(order, e.SchemaName)
		}
		s.Total++
	}
	summaries := make([]schemaSummary, 0, len(order))
	for _, name := range order {
		summaries = append(summaries, *bySchema[name])
	}
	resp.OK(c, gin.H{"list": all, "total": len(all), "schemas": summaries})
}

// BuildCatalog POST /api/mysql/:id/ai/catalog
//
// schema 指定库；all=true 时枚举整个连接的非系统库逐库生成（表结构指纹未变的表
// 直接跳过，单批失败只累加失败数，不影响其余批次），支撑 AI 跨库查询。
func (h *Handler) BuildCatalog(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req catalogReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	client, aiCfg, err := h.aiClient()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), catalogTotalTimeout)
	defer cancel()
	// 目录生成是逐批串行的模型调用，单看某一批看不出整体花费，故记录总耗时
	started := time.Now()

	// all=true：枚举整个连接的非系统库逐库生成；schemas 非空：按弹框勾选的库列表生成；
	// 否则只处理指定的一个库（解析逻辑与后台任务共用）
	schemas, code, err := h.resolveCatalogSchemas(ctx, conn, &req)
	if err != nil {
		resp.Fail(c, code, err.Error())
		return
	}

	states := make([]model.MySQLAICatalogState, 0, len(schemas))
	total := model.MySQLAIUsage{}
	for _, schema := range schemas {
		st, err := h.buildSchemaCatalog(ctx, conn, client, aiCfg, schema, req.Force, nil)
		if err != nil {
			// 单库失败（连不上 / 无权限）不拖垮其余库：记一条失败状态继续
			st = &model.MySQLAICatalogState{Schema: schema, Message: err.Error()}
		}
		states = append(states, *st)
		addUsage(&total, st.Usage)
	}
	total.ElapsedMs = time.Since(started).Milliseconds()

	generated, cached, failed := 0, 0, 0
	for _, st := range states {
		generated += st.Generated
		cached += st.Cached
		failed += st.Failed
	}
	message := fmt.Sprintf("共 %d 个库：新生成 %d 张表，命中缓存 %d，失败 %d",
		len(states), generated, cached, failed)
	if failed > 0 {
		message += "；存在失败，详情见各库状态"
	}
	resp.OK(c, gin.H{"schemas": states, "usage": total, "message": message})
}

// resolveCatalogSchemas 把请求里的 schema / schemas / all 归一成待处理的库列表。
// 同步接口（BuildCatalog）与后台任务（StartCatalogTask）共用；出错时一并给出业务码：
// 参数问题 400、连接 / 枚举失败 500。
func (h *Handler) resolveCatalogSchemas(ctx context.Context, conn *model.MySQLConn, req *catalogReq) ([]string, int, error) {
	switch {
	case len(req.Schemas) > 0:
		schemas := normalizePickedSchemas(req.Schemas)
		if len(schemas) == 0 {
			return nil, resp.CodeBadRequest, fmt.Errorf("勾选的库里没有可生成目录的库（系统库不参与生成）")
		}
		return schemas, 0, nil
	case req.All:
		sess, err := h.open(conn)
		if err != nil {
			return nil, resp.CodeInternal, fmt.Errorf("连接失败: %w", err)
		}
		names, err := listDatabases(ctx, sess.db)
		sess.Close()
		if err != nil {
			return nil, resp.CodeInternal, fmt.Errorf("读取库列表失败: %w", err)
		}
		schemas := []string{}
		for _, n := range names {
			if !isSystemSchema(n) {
				schemas = append(schemas, n)
			}
		}
		if len(schemas) == 0 {
			return nil, resp.CodeBadRequest, fmt.Errorf("连接里没有可生成目录的非系统库")
		}
		return schemas, 0, nil
	default:
		schema := strings.TrimSpace(req.Schema)
		if schema == "" {
			return nil, resp.CodeBadRequest, fmt.Errorf("请先选择要分析的库（或传 all=true / schemas 批量生成）")
		}
		return []string{schema}, 0, nil
	}
}

// buildSchemaCatalog 对单个库生成语义目录。原来内联在 BuildCatalog 里，
// 全库模式要逐库调用，故抽出来；状态逐批累加，失败只进 Message 不中断。
// onBatch 为批级进度回调（后台任务用来展示「第 n/N 批」并逐条攒生成记录），同步调用处传 nil。
func (h *Handler) buildSchemaCatalog(ctx context.Context, conn *model.MySQLConn,
	client *aiopenai.Client, aiCfg aiSettings, schema string, force bool, onBatch func(ev catalogBatchProgress)) (*model.MySQLAICatalogState, error) {

	sess, err := h.openForSchema(conn, schema)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	tables, err := listTables(ctx, sess.db, schema)
	if err != nil {
		return nil, fmt.Errorf("读取表列表失败: %w", err)
	}

	briefs := make([]tableBrief, 0, len(tables))
	fps := make([]string, 0, len(tables))
	for _, t := range tables {
		cols, err := listColumns(ctx, sess.db, schema, t.Name)
		if err != nil {
			return nil, fmt.Errorf("读取表 "+t.Name+" 字段失败: %w", err)
		}
		briefs = append(briefs, toBrief(t, cols))
		fps = append(fps, tableFingerprint(t, cols))
	}

	cached, err := h.aiRepo.ListCatalog(conn.ID, schema)
	if err != nil {
		return nil, fmt.Errorf("读取语义目录失败: %w", err)
	}
	byName := map[string]model.MySQLAICatalogEntry{}
	for _, e := range cached {
		byName[e.TableName] = e
	}

	state := model.MySQLAICatalogState{Schema: schema, Total: len(tables)}
	names := make([]string, 0, len(briefs))
	pending := []int{}
	for i, b := range briefs {
		names = append(names, b.Table)
		if e, hit := byName[b.Table]; hit && !force {
			if e.Fingerprint == fps[i] {
				state.Cached++
				continue
			}
			state.Stale++
		}
		pending = append(pending, i)
	}

	batches := batchIndexes(briefs, pending)
	interrupted := false
	for bi, batch := range batches {
		// 后台任务被停止 / 整体超时：剩余批次不再跑，已处理的批次已逐批落库
		if ctx.Err() != nil {
			interrupted = true
			break
		}
		state.Batches++
		askList := make([]tableBrief, 0, len(batch))
		for _, i := range batch {
			askList = append(askList, briefs[i])
		}
		// 每批一条调用记录：token 花费按批分摊，哪一批拖了后腿、哪一批报错都能看出来
		batchStarted := time.Now()
		trace := h.beginTrace(aiTraceParams{
			Kind:       model.AILogKindCatalog,
			Conn:       conn,
			Schema:     schema,
			Model:      aiCfg.Model,
			BaseURL:    aiCfg.BaseURL,
			BatchNo:    bi + 1,
			BatchTotal: len(batches),
			TableCount: len(batch),
		})
		res, replies, err := askCatalog(ctx, client, schema, askList)
		trace.fill(res)
		accumulateUsage(&state.Usage, res)
		if err != nil {
			trace.fail(err)
			trace.done()
			state.Failed += len(batch)
			if state.Message == "" {
				state.Message = err.Error()
			}
		} else {
			// 记录里存归纳摘要而非原始 JSON：一批的原始返回动辄上万字符，全存只会把记录撑大
			trace.row.Content = truncateRunes(catalogDigest(replies), logContentMax)
			trace.done()

			got := map[string]catalogReply{}
			for _, r := range replies {
				got[strings.TrimSpace(r.Table)] = r
			}
			for _, i := range batch {
				b := briefs[i]
				r, hit := got[b.Table]
				if !hit {
					state.Failed++
					continue
				}
				// 落库前对时间相关 int 列抽一条真实值，探测存的是秒还是毫秒（写穿到 brief 的列切片）
				probeTimestampUnits(ctx, sess.db, schema, b.Table, b.Columns)
				if err := h.aiRepo.UpsertCatalog(conn.ID, buildCatalogEntry(schema, b, fps[i], r)); err != nil {
					state.Failed++
					continue
				}
				state.Generated++
			}
		}
		if onBatch != nil {
			// 批处理进度（成功 / 失败都算处理过一批）；记录带批耗时与用量，弹框「生成记录」即收即显
			onBatch(catalogBatchProgress{
				Done:   bi + 1,
				Total:  len(batches),
				Record: catalogBatchRecord(schema, bi+1, len(batches), len(batch), batchStarted, res, err),
			})
		}
	}

	// 表被删 / 改名后，目录里的旧条目一并清掉
	if err := h.aiRepo.DropCatalogNotIn(conn.ID, schema, names); err != nil {
		state.Message = "清理失效条目失败: " + err.Error()
	}
	if interrupted {
		state.Message = fmt.Sprintf("已停止：处理到第 %d/%d 批（已生成 %d 张表，命中缓存 %d，失败 %d）",
			state.Batches, len(batches), state.Generated, state.Cached, state.Failed)
	} else if state.Message == "" {
		state.Message = fmt.Sprintf("共 %d 张表：新生成 %d，命中缓存 %d，结构变化 %d，失败 %d",
			state.Total, state.Generated, state.Cached, state.Stale, state.Failed)
	}
	return &state, nil
}

// addUsage 把单库目录生成的用量累加到全库总量上。
func addUsage(dst *model.MySQLAIUsage, src model.MySQLAIUsage) {
	dst.Calls += src.Calls
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
	if src.Source == "estimated" {
		dst.Source = "estimated"
	}
}

// isSystemSchema 系统库判定：全库目录与自动挑默认库共用（与前端 MYSQL_SYSTEM_DBS 同口径）。
func isSystemSchema(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "information_schema", "performance_schema", "mysql", "sys":
		return true
	}
	return false
}

// normalizePickedSchemas 清洗弹框勾选的库列表：去空白、去重、剔除系统库，保持勾选顺序。
func normalizePickedSchemas(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" || isSystemSchema(n) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// batchIndexes 按「表数量 + 字符数」切批；pending 为空时不发请求。
func batchIndexes(briefs []tableBrief, pending []int) [][]int {
	out := [][]int{}
	cur := []int{}
	chars := 0
	for _, i := range pending {
		n := briefCost(briefs[i])
		if len(cur) > 0 && (len(cur) >= catalogBatchTables || chars+n > catalogBatchChars) {
			out = append(out, cur)
			cur, chars = []int{}, 0
		}
		cur = append(cur, i)
		chars += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// askCatalog 一次请求归纳一批表；解析失败时也把客户端结果带回去，便于记录里留下本次用量。
// payload 用紧凑 JSON（非缩进）：缩进美化只是给眼睛看的，进模型就是白花的 token。
func askCatalog(ctx context.Context, client *aiopenai.Client, schema string,
	briefs []tableBrief) (*aiopenai.Result, []catalogReply, error) {
	payload, err := json.Marshal(briefs)
	if err != nil {
		return nil, nil, err
	}
	res, err := client.Chat(ctx, []aiopenai.Message{
		{Role: "system", Content: catalogSystemPrompt},
		{Role: "user", Content: fmt.Sprintf(catalogUserPrompt, schema, string(payload))},
	})
	if err != nil {
		return nil, nil, err
	}
	var replies []catalogReply
	if err := unmarshalModelJSON(res.Content, &replies); err != nil {
		return res, nil, fmt.Errorf("解析模型返回失败: %w", err)
	}
	return res, replies, nil
}

// catalogDigest 把一批归纳结果压成便于回看的摘要（表名：用途），供调用记录留档。
func catalogDigest(replies []catalogReply) string {
	var b strings.Builder
	for _, r := range replies {
		b.WriteString(r.Table)
		b.WriteString("：")
		b.WriteString(r.Purpose)
		b.WriteByte('\n')
	}
	return b.String()
}

// buildCatalogEntry 把模型返回对齐到真实字段：以库里的字段顺序为准，notes 按下标回填；
// 数量不符（模型没按约定给全）时整表退回原注释，避免错位把 A 字段的说明挂到 B 字段上。
// 字段的真实类型与时间戳单位随条目一并标记，供生成 SQL 时做时间格式自适应。
func buildCatalogEntry(schema string, b tableBrief, fp string, r catalogReply) *model.MySQLAICatalogEntry {
	aligned := len(r.Notes) == len(b.Columns)
	cols := make([]model.MySQLAIColumn, 0, len(b.Columns))
	for i, c := range b.Columns {
		note := ""
		if aligned {
			note = strings.TrimSpace(r.Notes[i])
		}
		if note == "" {
			note = c.Comment
		}
		cols = append(cols, model.MySQLAIColumn{Name: c.Name, Note: note, Type: c.Type, Unit: c.Unit})
	}
	return &model.MySQLAICatalogEntry{
		SchemaName:  schema,
		TableName:   b.Table,
		Purpose:     strings.TrimSpace(r.Purpose),
		Columns:     cols,
		Fingerprint: fp,
	}
}

// probeTimestampUnits 对时间相关的 int 列各抽一条正值样本，写回时间戳单位（s / ms）。
// 传进来的是 brief 的列切片，改动直接落回待入库的条目；探测不出（空表 / 超时 / 权限）留空，
// 生成 SQL 时按秒级兜底。
func probeTimestampUnits(ctx context.Context, db *sql.DB, schema, table string, cols []columnBrief) {
	for j := range cols {
		c := &cols[j]
		if c.Unit != "" || intFamily(c.Type) == "" || !looksTemporalColumn(c.Name, c.Comment) {
			continue
		}
		c.Unit = probeTimestampUnit(ctx, db, schema, table, c.Name)
	}
}

// probeTimestampUnit 取该列第一条正值判断单位：>= 1e11（12 位起）必为毫秒
// （毫秒到达 1e11 是 1973 年，秒级要 5138 年才够 12 位）；>= 1e8（1973 年）判秒。
// MAX_EXECUTION_TIME 兜底防大表无索引列拖慢建目录；判不了返回空。
func probeTimestampUnit(ctx context.Context, db *sql.DB, schema, table, col string) string {
	q := fmt.Sprintf("SELECT /*+ MAX_EXECUTION_TIME(2000) */ `%s` FROM `%s`.`%s` WHERE `%s` > 0 LIMIT 1",
		escapeIdent(col), escapeIdent(schema), escapeIdent(table), escapeIdent(col))
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, q).Scan(&v); err != nil || !v.Valid {
		return ""
	}
	return timestampUnit(v.Int64)
}

// timestampUnit 按量级判定时间戳单位：12 位起为毫秒，8 位起为秒，其余（0 / 小值）判不了。
func timestampUnit(v int64) string {
	switch {
	case v >= 100_000_000_000:
		return "ms"
	case v >= 100_000_000:
		return "s"
	}
	return ""
}

// escapeIdent 反引号转义：名字来自 information_schema，防怪名破坏探测语句。
func escapeIdent(s string) string {
	return strings.ReplaceAll(s, "`", "``")
}
