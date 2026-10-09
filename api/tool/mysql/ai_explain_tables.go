// ai_explain_tables.go 执行计划分析的「相关表结构与统计」上下文组装。
//
// 从被分析的 SQL 里解析表引用（含别名回映，传统表名 / 别名两种计划输出都能对上），
// 与执行计划里的表名对齐后，逐表从 information_schema 取：字段定义、索引定义、
// 估算行数与数据 / 索引大小。全部是结构元信息——与「重跑取计划」同一安全口径：
// 不含任何业务行数据，表数据明细绝不离开本机。
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"infra-ops/model"
)

const (
	explainMaxTables  = 6  // 随上下文送出的相关表上限
	explainMaxColumns = 40 // 每表列数上限（超出截断并注明）
	explainMaxIndexes = 12 // 每表索引数上限（超出截断并注明）
)

// explainRefRe 抓 FROM / JOIN / UPDATE / INTO 之后的表引用（只吃到表名本身，
// 别名另行探测——把关键字（JOIN / WHERE…）吃进别名会吞掉它引出的下一张表）。
var explainRefRe = regexp.MustCompile("(?i)\\b(?:FROM|JOIN|UPDATE|INTO)\\s+(`[^`]+`|[A-Za-z0-9_$]+)(?:\\s*\\.\\s*(`[^`]+`|[A-Za-z0-9_$]+))?")

// explainWordRe 词切分：反引号标识符整体算一个词，其余按字母数字下划线美元符。
var explainWordRe = regexp.MustCompile("`[^`]*`|[A-Za-z0-9_$]+")

// explainTableNameRe JSON 格式执行计划里的真实表名。
var explainTableNameRe = regexp.MustCompile(`"table_name"\s*:\s*"([^"]+)"`)

// explainAliasSkip 这些词跟在表引用后是语法关键字，不是别名。
var explainAliasSkip = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true, "LIMIT": true, "WINDOW": true,
	"ON": true, "USING": true, "INNER": true, "LEFT": true, "RIGHT": true, "CROSS": true,
	"OUTER": true, "NATURAL": true, "STRAIGHT_JOIN": true, "JOIN": true, "UNION": true,
	"SET": true, "VALUES": true, "VALUE": true, "FOR": true, "LOCK": true, "IGNORE": true,
	"USE": true, "FORCE": true, "PARTITION": true, "AS": true, "SELECT": true,
}

// explainTableRef 解析出的一个表引用。
type explainTableRef struct {
	Schema string // 显式库前缀（可空）
	Table  string
	Alias  string // 可空
}

// explainIdent 反引号标识符解码：剥外层反引号，`` 还原成 `。
func explainIdent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		s = s[1 : len(s)-1]
	}
	return strings.ReplaceAll(s, "``", "`")
}

// explainTakeWord 从 s 头部（允许前导空白）取一个词，返回词与剩余文本。
func explainTakeWord(s string) (string, string) {
	s = strings.TrimLeft(s, " \t\r\n")
	if s == "" {
		return "", ""
	}
	loc := explainWordRe.FindStringIndex(s)
	if loc == nil || loc[0] != 0 {
		return "", s
	}
	return s[:loc[1]], s[loc[1]:]
}

// explainRefs 解析 SQL 里的表引用：同时给「别名 → 表」与「表名 → 表」两张映射。
// 计划输出究竟显示别名还是真实表名（传统格式存在口径差异）都能对上；
// FROM a, b 逗号列表不收敛（逗号无法与函数参数区分），这类无别名场景由原名兜底。
func explainRefs(stmt string) (byAlias map[string]explainTableRef, plain map[string]explainTableRef) {
	s := blockCommentRe.ReplaceAllString(stmt, " ")
	s = lineCommentRe.ReplaceAllString(s, " ")
	byAlias = map[string]explainTableRef{}
	plain = map[string]explainTableRef{}
	for _, m := range explainRefRe.FindAllStringSubmatchIndex(s, -1) {
		ref := explainTableRef{}
		if m[4] >= 0 { // 带库前缀：`库`.`表`
			ref.Schema = explainIdent(s[m[2]:m[3]])
			ref.Table = explainIdent(s[m[4]:m[5]])
		} else {
			ref.Table = explainIdent(s[m[2]:m[3]])
		}
		// 别名探测：表引用后允许 AS 或直接跟一个非关键字词
		alias := ""
		w1, rest := explainTakeWord(s[m[1]:])
		if strings.EqualFold(w1, "AS") {
			w1, _ = explainTakeWord(rest)
		}
		if w1 != "" && !explainAliasSkip[strings.ToUpper(w1)] {
			alias = explainIdent(w1)
		}
		if alias != "" && explainAliasSkip[strings.ToUpper(alias)] {
			alias = ""
		}
		ref.Alias = alias
		if ref.Table == "" {
			continue
		}
		plain[strings.ToLower(ref.Table)] = ref
		if alias != "" {
			byAlias[strings.ToLower(alias)] = ref
		}
	}
	return byAlias, plain
}

// explainPlanTables 从计划结果里收集被引用的表名：传统格式看 table 列；
// JSON 格式（EXPLAIN FORMAT=JSON）从单列文本里抽 table_name。伪表（<derived2> 等）跳过。
func explainPlanTables(res *model.MySQLQueryResult) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || strings.HasPrefix(v, "<") { // <derived2> / <union1,2> / <subquery2>
			return
		}
		low := strings.ToLower(v)
		if seen[low] {
			return
		}
		seen[low] = true
		out = append(out, v)
	}
	ti := -1 // table 列下标
	for i, c := range res.Columns {
		if strings.EqualFold(c.Name, "table") {
			ti = i
			break
		}
	}
	for _, row := range res.Rows {
		if ti >= 0 && ti < len(row) {
			add(explainValue(row[ti]))
		}
		for _, cell := range row {
			s := explainValue(cell)
			if !strings.Contains(s, "table_name") {
				continue
			}
			for _, m := range explainTableNameRe.FindAllStringSubmatch(s, -1) {
				add(m[1])
			}
		}
	}
	return out
}

// explainResolve 把计划里的表名映射成真实表：别名优先、原名兜底、默认库填充，去重并限量。
func explainResolve(cands []string, byAlias, plain map[string]explainTableRef, defSchema string) []explainTableRef {
	out := make([]explainTableRef, 0, len(cands))
	seen := map[string]bool{}
	for _, c := range cands {
		c = strings.TrimSpace(c)
		if c == "" || strings.HasPrefix(c, "<") {
			continue
		}
		low := strings.ToLower(c)
		ref, ok := byAlias[low]
		if !ok {
			ref, ok = plain[low]
		}
		if !ok {
			ref = explainTableRef{Table: c}
		}
		if ref.Schema == "" {
			ref.Schema = defSchema
		}
		key := strings.ToLower(ref.Schema + "." + ref.Table)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
		if len(out) >= explainMaxTables {
			break
		}
	}
	return out
}

// explainTablesContext 组装相关表上下文：计划取表 → 别名回映 → 逐表取结构统计。
// 任何一张表取不到都只降级为 note，不阻断分析。
func explainTablesContext(ctx context.Context, db *sql.DB, res *model.MySQLQueryResult, stmt, schema string) []model.MySQLExplainTable {
	defSchema := strings.TrimSpace(schema)
	if defSchema == "" {
		// 会话未显式指定库：以连接实际选中的库为准
		var name sql.NullString
		if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); err == nil && name.Valid {
			defSchema = name.String
		}
	}
	byAlias, plain := explainRefs(stmt)
	refs := explainResolve(explainPlanTables(res), byAlias, plain, defSchema)
	out := make([]model.MySQLExplainTable, 0, len(refs))
	for _, ref := range refs {
		out = append(out, explainOneTable(ctx, db, ref))
	}
	return out
}

// explainOneTable 取单张表的统计 / 字段 / 索引（全部来自 information_schema）。
func explainOneTable(ctx context.Context, db *sql.DB, ref explainTableRef) model.MySQLExplainTable {
	t := model.MySQLExplainTable{Schema: ref.Schema, Name: ref.Table, Columns: []string{}, Indexes: []string{}}
	if ref.Schema == "" {
		t.Note = "会话未选择库，取不到表结构"
		return t
	}

	// 表统计：估算行数 + 数据 / 索引长度
	err := db.QueryRowContext(ctx, `
		SELECT IFNULL(ENGINE, ''), IFNULL(TABLE_ROWS, 0), IFNULL(DATA_LENGTH, 0), IFNULL(INDEX_LENGTH, 0)
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`,
		ref.Schema, ref.Table).Scan(&t.Engine, &t.Rows, &t.DataLength, &t.IndexLength)
	if err == sql.ErrNoRows {
		t.Note = "information_schema 中没有这张表（可能是派生表 / 临时表，或已删除）"
		return t
	}
	if err != nil {
		t.Note = "读取表统计失败: " + err.Error()
		return t
	}

	// 字段定义
	rows, err := db.QueryContext(ctx, `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, IFNULL(COLUMN_COMMENT, '')
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`, ref.Schema, ref.Table)
	if err != nil {
		explainAppendNote(&t, "读取字段失败: "+err.Error())
		return t
	}
	total := 0
	for rows.Next() {
		var name, typ, nullable, key, comment string
		if err := rows.Scan(&name, &typ, &nullable, &key, &comment); err != nil {
			continue
		}
		total++
		if len(t.Columns) >= explainMaxColumns {
			continue
		}
		line := name + " " + typ
		if nullable == "NO" {
			line += " NOT NULL"
		}
		if key != "" {
			line += " " + key
		}
		if comment != "" {
			line += " " + comment
		}
		t.Columns = append(t.Columns, line)
	}
	rows.Close()
	if total > explainMaxColumns {
		explainAppendNote(&t, fmt.Sprintf("列过多，仅列前 %d 列（共 %d 列）", explainMaxColumns, total))
	}

	// 索引定义：按索引名聚合列，附带基数（估算，用于判断选择性）
	irows, err := db.QueryContext(ctx, `
		SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME, IFNULL(CARDINALITY, 0)
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`, ref.Schema, ref.Table)
	if err != nil {
		explainAppendNote(&t, "读取索引失败: "+err.Error())
		return t
	}
	type idxAgg struct {
		unique bool
		card   int64
		cols   []string
	}
	agg := map[string]*idxAgg{}
	order := []string{}
	for irows.Next() {
		var name, col string
		var nonUnique int
		var card int64
		if err := irows.Scan(&name, &nonUnique, &col, &card); err != nil {
			continue
		}
		a := agg[name]
		if a == nil {
			a = &idxAgg{unique: nonUnique == 0}
			agg[name] = a
			order = append(order, name)
		}
		a.cols = append(a.cols, col)
		if card > a.card {
			a.card = card
		}
	}
	irows.Close()
	for i, name := range order {
		if i >= explainMaxIndexes {
			explainAppendNote(&t, fmt.Sprintf("索引过多，仅列前 %d 个", explainMaxIndexes))
			break
		}
		a := agg[name]
		line := name
		if a.unique {
			line += " UNIQUE"
		}
		line += " (" + strings.Join(a.cols, ", ") + ")"
		if a.card > 0 {
			line += fmt.Sprintf(" 基数≈%d", a.card)
		}
		t.Indexes = append(t.Indexes, line)
	}
	return t
}

// explainAppendNote 汇总降级说明（多条原因用分号连接）。
func explainAppendNote(t *model.MySQLExplainTable, s string) {
	if t.Note != "" {
		t.Note += "；" + s
	} else {
		t.Note = s
	}
}

// explainTablesText 把表结构统计拼成送模型的文本；没有可解析的表时给说明句。
func explainTablesText(tables []model.MySQLExplainTable) string {
	if len(tables) == 0 {
		return "（未从执行计划里解析出相关表，或会话未选择库）"
	}
	var b strings.Builder
	for _, t := range tables {
		prefix := ""
		if t.Schema != "" {
			prefix = t.Schema + "."
		}
		engine := t.Engine
		if engine == "" {
			engine = "—"
		}
		fmt.Fprintf(&b, "表 %s%s · %s · 估算行数 %d · 数据 %s · 索引 %s\n",
			prefix, t.Name, engine, t.Rows, explainBytes(t.DataLength), explainBytes(t.IndexLength))
		for _, c := range t.Columns {
			b.WriteString("  列 " + c + "\n")
		}
		for _, x := range t.Indexes {
			b.WriteString("  索引 " + x + "\n")
		}
		if t.Note != "" {
			b.WriteString("  注：" + t.Note + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// explainBytes 字节转可读大小（与前端 fmtBytes 同口径）。
func explainBytes(v int64) string {
	f := float64(v)
	switch {
	case v < 1024:
		return fmt.Sprintf("%d B", v)
	case f < 1048576:
		return fmt.Sprintf("%.1f KB", f/1024)
	case f < 1073741824:
		return fmt.Sprintf("%.1f MB", f/1048576)
	default:
		return fmt.Sprintf("%.1f GB", f/1073741824)
	}
}
