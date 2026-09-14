// Schema 感知的 KQL → ES DSL 编译（B7）。入参必须是 Schema（§7，修复缺陷 1/4 的结构性前提）。
// 生成器红线（§4）：should 必带 minimum_should_match；禁 nil 子句；multi_match.fields 取真实候选。
package es

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"infra-ops/model"
)

// esResultWindow 结果窗口：一次拉满（§11），从不向 ES 传 offset。
const esResultWindow = 1000

// Schema 视图字段表（编译 KQL 的前置依赖，§5.5）。
type Schema struct {
	ViewID    int64
	Pattern   string
	TimeField string
	Fields    []model.ESViewField
	ByPath    map[string]*model.ESViewField
	Truncated bool
}

func BuildSchema(view *model.ESView, fields []model.ESViewField) *Schema {
	s := &Schema{ViewID: view.ID, Pattern: view.IndexPattern, TimeField: view.TimeField,
		Fields: fields, ByPath: make(map[string]*model.ESViewField, len(fields))}
	for i := range fields {
		s.ByPath[fields[i].Path] = &fields[i]
	}
	return s
}

// dslErr KQL 语义/取值编译错误（4001/4002），携带位置供前端定位。
type dslErr struct {
	code int
	msg  string
	pos  int
	len  int
}

func (e *dslErr) Error() string { return e.msg }

func semErr(code, pos, length int, format string, args ...interface{}) *dslErr {
	return &dslErr{code: code, msg: fmt.Sprintf(format, args...), pos: pos, len: length}
}

var numericTypes = map[string]bool{
	"long": true, "integer": true, "short": true, "byte": true,
	"float": true, "double": true, "scaled_float": true, "half_float": true,
}

// ---------- 入口 ----------

// compileQuery 把 KQL 编译为 ES query 段。kql 为空 → 返回 nil（match_all 语义）。
func compileQuery(kql string, schema *Schema) (map[string]interface{}, *dslErr) {
	if strings.TrimSpace(kql) == "" {
		return nil, nil
	}
	ast, perr := parseKQL(kql)
	if perr != nil {
		return nil, &dslErr{code: esCodeKQLSyntax, msg: perr.Error(), pos: perr.pos, len: perr.len}
	}
	return nodeToQuery(ast, schema)
}

// ---------- bool 生成（§8.2） ----------

// nodeToQuery 递归生成，AND/OR/NOT 收敛进同一层 bool（§4 缺陷 6：should 必带 minimum_should_match:1）。
func nodeToQuery(n *astNode, schema *Schema) (map[string]interface{}, *dslErr) {
	switch n.kind {
	case nodeTerm:
		return termToQuery(n, schema)
	case nodeNot:
		inner, err := nodeToQuery(n.left, schema)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"bool": map[string]interface{}{"must_not": []interface{}{inner}},
		}, nil
	}
	// nodeAnd / nodeOr：收集同层子句。子句归属由**父节点**类型决定。
	isAnd := n.kind == nodeAnd
	var must, should, mustNot []interface{}
	children := flattenSameKind(n)
	for _, ch := range children {
		if ch.kind == nodeNot {
			inner, err := nodeToQuery(ch.left, schema)
			if err != nil {
				return nil, err
			}
			mustNot = append(mustNot, inner)
			continue
		}
		if !isAnd && ch.kind == nodeAnd {
			// OR 分支下的 AND 子组：把它的 must 子句提升到本层 must（同层收敛，不丢子句）
			for _, g := range flattenSameKind(ch) {
				if g.kind == nodeNot {
					inner, err := nodeToQuery(g.left, schema)
					if err != nil {
						return nil, err
					}
					mustNot = append(mustNot, inner)
					continue
				}
				q, err := nodeToQuery(g, schema)
				if err != nil {
					return nil, err
				}
				must = append(must, q)
			}
			continue
		}
		q, err := nodeToQuery(ch, schema)
		if err != nil {
			return nil, err
		}
		if isAnd {
			must = append(must, q)
		} else {
			should = append(should, q)
		}
	}
	b := map[string]interface{}{}
	if len(must) > 0 {
		b["must"] = must
	}
	if len(mustNot) > 0 {
		b["must_not"] = mustNot
	}
	if len(should) > 0 {
		b["should"] = should
		b["minimum_should_match"] = 1
	}
	return map[string]interface{}{"bool": b}, nil
}

func flattenSameKind(n *astNode) []*astNode {
	if n.kind != nodeAnd && n.kind != nodeOr {
		return []*astNode{n}
	}
	out := []*astNode{}
	var walk func(x *astNode)
	walk = func(x *astNode) {
		if x.kind == n.kind {
			walk(x.left)
			walk(x.right)
			return
		}
		out = append(out, x)
	}
	walk(n)
	return out
}

// ---------- term 分派（§9 表格） ----------

func termToQuery(n *astNode, schema *Schema) (map[string]interface{}, *dslErr) {
	if n.field == "" {
		return bareValueQuery(n, schema)
	}
	f := schema.ByPath[n.field]
	if f == nil {
		return nil, semErr(esCodeFieldNotExist, n.pos, len(n.field),
			"字段 %s 不存在，是否想用 %s？", n.field, nearestField(schema, n.field))
	}
	if n.value == "*" && n.wildcard && (n.op == "" || n.op == ":") {
		return wrapNot(n.op, map[string]interface{}{"exists": map[string]interface{}{"field": n.field}}), nil
	}
	if hasType(f, "nested") {
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.field),
			"字段 %s 是 nested 类型，一期不支持嵌套查询", n.field)
	}
	if f.TypeConflict { // 多类型 → 最宽公共算子（§9）
		return conflictQuery(n, f)
	}
	typ := f.Types[0]
	switch {
	case typ == "text":
		return textQuery(n, f)
	case typ == "keyword":
		return keywordQuery(n, f)
	case numericTypes[typ]:
		return numericQuery(n, f, typ)
	case typ == "date":
		return dateQuery(n, f)
	case typ == "boolean":
		return boolQuery(n, f)
	case typ == "ip":
		return ipQuery(n, f)
	}
	return nil, semErr(esCodeOpNotSupported, n.pos, len(n.field),
		"字段 %s 的类型 %s 暂不支持该算子", n.field, typ)
}

func wrapNot(op string, q map[string]interface{}) map[string]interface{} {
	if op != "!=" {
		return q
	}
	return map[string]interface{}{"bool": map[string]interface{}{"must_not": []interface{}{q}}}
}

func hasType(f *model.ESViewField, typ string) bool {
	for _, t := range f.Types {
		if t == typ {
			return true
		}
	}
	return false
}

// bareValueQuery 裸值 → multi_match，fields 取字段表全部 searchable 字段（§4 缺陷 4）。
func bareValueQuery(n *astNode, schema *Schema) (map[string]interface{}, *dslErr) {
	if len(schema.Fields) == 0 {
		return nil, &dslErr{code: esCodeNoTimeField, pos: n.pos, len: 1,
			msg: "视图字段表为空，请先刷新视图字段"}
	}
	fields := []string{}
	for i := range schema.Fields {
		if schema.Fields[i].Searchable {
			fields = append(fields, schema.Fields[i].Path)
		}
	}
	if len(fields) == 0 {
		return nil, semErr(esCodeOpNotSupported, n.pos, 1, "字段表中没有可搜索字段")
	}
	mm := map[string]interface{}{
		"query": n.value, "type": "best_fields", "lenient": true, "fields": fields,
	}
	if n.quoted {
		mm["type"] = "phrase"
	}
	return map[string]interface{}{"multi_match": mm}, nil
}

func textQuery(n *astNode, f *model.ESViewField) (map[string]interface{}, *dslErr) {
	switch {
	case n.op == ">=" || n.op == ">" || n.op == "<=" || n.op == "<":
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.field), "text 字段不支持范围比较")
	case n.wildcard: // query_string + analyze_wildcard
		return wrapNot(n.op, map[string]interface{}{"query_string": map[string]interface{}{
			"query": n.value, "default_field": f.Path, "analyze_wildcard": true}}), nil
	case n.quoted: // 缺陷 1：引号 → match_phrase，与裸值产出不同 DSL
		return wrapNot(n.op, map[string]interface{}{"match_phrase": map[string]interface{}{f.Path: n.value}}), nil
	default: // 裸值 → match
		return wrapNot(n.op, map[string]interface{}{"match": map[string]interface{}{f.Path: n.value}}), nil
	}
}

func keywordQuery(n *astNode, f *model.ESViewField) (map[string]interface{}, *dslErr) {
	switch {
	case n.op == ">=" || n.op == ">" || n.op == "<=" || n.op == "<":
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.field), "keyword 字段不支持范围比较")
	case n.wildcard:
		return wrapNot(n.op, map[string]interface{}{"wildcard": map[string]interface{}{f.Path: n.value}}), nil
	default: // keyword 精确匹配：裸值与引号值同语义（term）
		return wrapNot(n.op, map[string]interface{}{"term": map[string]interface{}{f.Path: n.value}}), nil
	}
}

func numericQuery(n *astNode, f *model.ESViewField, typ string) (map[string]interface{}, *dslErr) {
	if n.wildcard {
		return nil, semErr(esCodeKQLSyntax, n.pos, len(n.value), "数值字段不支持通配符")
	}
	v, err := strconv.ParseFloat(n.value, 64)
	if err != nil {
		return nil, semErr(esCodeKQLSyntax, n.pos, len(n.value), "值 %q 不是合法数值", n.value)
	}
	switch n.op {
	case ">=", ">", "<=", "<":
		r := map[string]interface{}{f.Path: map[string]interface{}{rangeOp(n.op): v}}
		return map[string]interface{}{"range": r}, nil
	default: // : = !=
		return wrapNot(n.op, map[string]interface{}{"term": map[string]interface{}{f.Path: v}}), nil
	}
}

func dateQuery(n *astNode, f *model.ESViewField) (map[string]interface{}, *dslErr) {
	if n.quoted {
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.field), "date 字段不支持引号包裹的值")
	}
	if n.wildcard {
		return nil, semErr(esCodeKQLSyntax, n.pos, len(n.value), "date 字段不支持通配符")
	}
	ms, span, derr := parseDateLiteral(n.value)
	if derr != nil {
		return nil, semErr(esCodeKQLSyntax, n.pos, len(n.value), "%s", derr.Error())
	}
	if n.op == ">=" || n.op == ">" || n.op == "<=" || n.op == "<" {
		r := map[string]interface{}{f.Path: map[string]interface{}{rangeOp(n.op): ms}}
		return map[string]interface{}{"range": r}, nil
	}
	// 点查询：[ms, ms+span) 下半开，与全局区间语义同构
	r := map[string]interface{}{f.Path: map[string]interface{}{"gte": ms, "lt": ms + span}}
	return wrapNot(n.op, map[string]interface{}{"range": r}), nil
}

func rangeOp(op string) string {
	switch op {
	case ">=":
		return "gte"
	case ">":
		return "gt"
	case "<=":
		return "lte"
	default:
		return "lt"
	}
}

func boolQuery(n *astNode, f *model.ESViewField) (map[string]interface{}, *dslErr) {
	if n.wildcard || n.quoted {
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.value), "boolean 字段仅接受 true/false")
	}
	v := strings.ToLower(n.value)
	if v != "true" && v != "false" {
		return nil, semErr(esCodeKQLSyntax, n.pos, len(n.value), "boolean 字段的值必须是 true 或 false")
	}
	return wrapNot(n.op, map[string]interface{}{"term": map[string]interface{}{f.Path: v == "true"}}), nil
}

func ipQuery(n *astNode, f *model.ESViewField) (map[string]interface{}, *dslErr) {
	if n.wildcard {
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.value), "ip 字段不支持通配符")
	}
	switch n.op {
	case ">=", ">", "<=", "<": // 含 CIDR
		r := map[string]interface{}{f.Path: map[string]interface{}{rangeOp(n.op): n.value}}
		return map[string]interface{}{"range": r}, nil
	default:
		return wrapNot(n.op, map[string]interface{}{"term": map[string]interface{}{f.Path: n.value}}), nil
	}
}

func conflictQuery(n *astNode, f *model.ESViewField) (map[string]interface{}, *dslErr) {
	switch {
	case n.op == ">=" || n.op == ">" || n.op == "<=" || n.op == "<" || n.wildcard:
		return nil, semErr(esCodeOpNotSupported, n.pos, len(n.field),
			"字段 %s 存在多类型冲突，不支持该算子", f.Path)
	case n.quoted:
		return wrapNot(n.op, map[string]interface{}{"match_phrase": map[string]interface{}{
			f.Path: map[string]interface{}{"query": n.value, "lenient": true}}}), nil
	default:
		return wrapNot(n.op, map[string]interface{}{"match": map[string]interface{}{
			f.Path: map[string]interface{}{"query": n.value, "lenient": true}}}), nil
	}
}

func nearestField(schema *Schema, name string) string {
	best, bestDist := "", len(name)*2+3
	for i := range schema.Fields {
		d := editDistance(strings.ToLower(name), strings.ToLower(schema.Fields[i].Path))
		if d < bestDist {
			best, bestDist = schema.Fields[i].Path, d
		}
	}
	if best == "" {
		return "(无近似字段)"
	}
	return best
}

func editDistance(a, b string) int {
	la, lb := len(a), len(b)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// ---------- 日期字面量（KQL 内，人类可读，§9） ----------

var reRelTime = regexp.MustCompile(`^now(?:-([1-9]\d*)([smhdw]))?$`)

var relUnits = map[byte]time.Duration{
	's': time.Second, 'm': time.Minute, 'h': time.Hour,
	'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour,
}

// parseDateLiteral 解析 KQL 日期字面量（now-1d / 2026-09-11 / 2026-09-11 17:00:00）→ 毫秒点与点查询跨度。
func parseDateLiteral(s string) (int64, int64, error) {
	if m := reRelTime.FindStringSubmatch(s); m != nil {
		ms := time.Now().In(beijing).UnixMilli()
		span := int64(1)
		if m[1] != "" {
			n, _ := strconv.Atoi(m[1])
			ms -= int64(relUnits[m[2][0]]/time.Millisecond) * int64(n)
		}
		return ms, span, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, beijing); err == nil {
		return t.UnixMilli(), 86400000, nil // [当日 00:00, 次日 00:00)
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, beijing); err == nil {
		return t.UnixMilli(), 1000, nil // [该秒, 下一秒)
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, beijing); err == nil {
		return t.UnixMilli(), 1000, nil
	}
	return 0, 0, fmt.Errorf("无法识别的日期字面量 %q（支持 now-1h / 2026-09-11 / 2026-09-11 17:00:00）", s)
}

// ---------- 搜索体组装（§10） ----------

// BuildSearchBody 组装 /{pattern}/_search 请求体（时间 filter gte/lt 下半开；sort/_source/highlight 取自字段表）。
func BuildSearchBody(schema *Schema, kql string, startMs, endMs int64,
	columns []string, histogramInterval string) (map[string]interface{}, *dslErr) {
	q, derr := compileQuery(kql, schema)
	if derr != nil {
		return nil, derr
	}
	inner := map[string]interface{}{}
	if q != nil {
		inner["must"] = []interface{}{q}
	}
	inner["filter"] = []interface{}{
		map[string]interface{}{"range": map[string]interface{}{
			schema.TimeField: map[string]interface{}{"gte": startMs, "lt": endMs},
		}},
	}
	body := map[string]interface{}{
		"query":            map[string]interface{}{"bool": inner},
		"from":             0,
		"size":             esResultWindow,
		"track_total_hits": true,
		"sort": []interface{}{
			map[string]interface{}{schema.TimeField: map[string]interface{}{"order": "desc"}},
			map[string]interface{}{"_doc": map[string]interface{}{"order": "desc"}},
		},
	}
	if len(columns) > 0 {
		body["_source"] = columns
	}
	if hl := highlightFields(columns, schema); len(hl) > 0 {
		body["highlight"] = map[string]interface{}{
			"fields": hl, "pre_tags": []string{"<mark>"}, "post_tags": []string{"</mark>"},
		}
	}
	if histogramInterval != "" {
		body["aggs"] = map[string]interface{}{
			"histogram": map[string]interface{}{
				"date_histogram": map[string]interface{}{
					"field": schema.TimeField, "fixed_interval": histogramInterval,
					"time_zone": "+08:00", "min_doc_count": 0,
					"extended_bounds": map[string]interface{}{"min": startMs, "max": endMs},
				},
			},
		}
	}
	return body, nil
}

func highlightFields(columns []string, schema *Schema) map[string]interface{} {
	out := map[string]interface{}{}
	if len(columns) == 0 {
		return out
	}
	paths := append([]string(nil), columns...)
	sort.Strings(paths)
	for _, p := range paths {
		if f := schema.ByPath[p]; f != nil && hasType(f, "text") {
			out[p] = map[string]interface{}{"number_of_fragments": 2, "fragment_size": 150}
		}
	}
	return out
}
