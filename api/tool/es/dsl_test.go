// B7 验收：§9 类型分派表逐行用例 + should/minimum_should_match 强制 + 时间 filter 形态校验。
package es

import (
	"strings"
	"testing"

	"infra-ops/model"
)

// mkSchema 构造测试字段表。
func mkSchema() *Schema {
	fields := []model.ESViewField{
		{Path: "@timestamp", Types: []string{"date"}, Searchable: true},
		{Path: "env", Types: []string{"keyword"}, Searchable: true, Aggregatable: true},
		{Path: "level", Types: []string{"keyword"}, Searchable: true, Aggregatable: true},
		{Path: "message", Types: []string{"text"}, Searchable: true},
		{Path: "status", Types: []string{"long"}, Searchable: true, Aggregatable: true},
		{Path: "score", Types: []string{"double"}, Searchable: true},
		{Path: "is_err", Types: []string{"boolean"}, Searchable: true},
		{Path: "ip", Types: []string{"ip"}, Searchable: true},
		{Path: "blob", Types: []string{"text", "keyword"}, Searchable: true, TypeConflict: true},
		{Path: "nested_field", Types: []string{"nested"}, Searchable: true},
	}
	view := &model.ESView{ID: 3, IndexPattern: "nginx-access-*", TimeField: "@timestamp"}
	return BuildSchema(view, fields)
}

func mustCompile(t *testing.T, kql string) map[string]interface{} {
	t.Helper()
	q, err := compileQuery(kql, mkSchema())
	if err != nil {
		t.Fatalf("编译 %q 失败: %v", kql, err)
	}
	return q
}

func getBool(t *testing.T, q map[string]interface{}) map[string]interface{} {
	t.Helper()
	b, ok := q["bool"].(map[string]interface{})
	if !ok {
		t.Fatalf("期望 bool 查询，得到 %#v", q)
	}
	return b
}

// ---------- text / keyword ----------

// 缺陷 1 正向锁定：裸值 match 与引号 match_phrase 产出不同 DSL。
func TestDSLQuotedVsBare(t *testing.T) {
	bare := mustCompile(t, "message:failed")
	quoted := mustCompile(t, `message:"failed to connect"`)
	if _, ok := bare["match"]; !ok {
		t.Fatalf("裸值应为 match: %#v", bare)
	}
	if _, ok := quoted["match_phrase"]; !ok {
		t.Fatalf("引号值应为 match_phrase: %#v", quoted)
	}
}

func TestDSLKeywordTerm(t *testing.T) {
	q := mustCompile(t, "env:prod")
	if _, ok := q["term"]; !ok {
		t.Fatalf("keyword 应为 term: %#v", q)
	}
}

func TestDSLKeywordWildcard(t *testing.T) {
	q := mustCompile(t, "env:prod*")
	if _, ok := q["wildcard"]; !ok {
		t.Fatalf("keyword 通配应为 wildcard: %#v", q)
	}
}

func TestDSLTextRangeRejected(t *testing.T) {
	if _, err := compileQuery("message:>=abc", mkSchema()); err == nil {
		t.Fatal("text 范围比较应报 4002")
	}
}

// ---------- 数值 ----------

func TestDSLNumericTermAndRange(t *testing.T) {
	q := mustCompile(t, "status:500")
	if _, ok := q["term"]; !ok {
		t.Fatalf("数值 : 应为 term: %#v", q)
	}
	r := mustCompile(t, "status:>=500")
	rg, ok := r["range"].(map[string]interface{})
	if !ok {
		t.Fatalf("数值 >= 应为 range: %#v", r)
	}
	inner := rg["status"].(map[string]interface{})
	if inner["gte"] != float64(500) {
		t.Fatalf("range 边界错误: %#v", inner)
	}
}

func TestDSLNumericBadValue(t *testing.T) {
	_, err := compileQuery("status:abc", mkSchema())
	if err == nil || !strings.Contains(err.Error(), "不是合法数值") {
		t.Fatalf("数值强转失败应报 4001: %v", err)
	}
}

// ---------- date ----------

func TestDSLDateLiteral(t *testing.T) {
	r := mustCompile(t, "@timestamp:>=2026-09-11")
	rg := r["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})
	ms, ok := rg["gte"].(int64)
	if !ok || ms != 1789056000000 { // 2026-09-11 00:00 +08:00
		t.Fatalf("date 字面量应冻结为毫秒 1789056000000: %#v", rg)
	}
}

func TestDSLDateRelative(t *testing.T) {
	r := mustCompile(t, "@timestamp:>=now-1d")
	rg := r["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})
	if _, ok := rg["gte"].(int64); !ok {
		t.Fatalf("now-1d 应编译为整数毫秒: %#v", rg)
	}
}

func TestDSLDatePointQuery(t *testing.T) {
	r := mustCompile(t, "@timestamp:2026-09-11")
	rg := r["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})
	if rg["gte"].(int64) != 1789056000000 || rg["lt"].(int64) != 1789056000000+86400000 {
		t.Fatalf("日期点查询应为 [当日 00:00, 次日 00:00): %#v", rg)
	}
}

// ---------- boolean / ip / nested / conflict ----------

func TestDSLBoolean(t *testing.T) {
	q := mustCompile(t, "is_err:true")
	tq := q["term"].(map[string]interface{})["is_err"]
	if tq != true {
		t.Fatalf("boolean 应为 bool term: %#v", tq)
	}
	if _, err := compileQuery("is_err:maybe", mkSchema()); err == nil {
		t.Fatal("boolean 非法值应报错")
	}
}

func TestDSLIpCIDR(t *testing.T) {
	r := mustCompile(t, "ip:>=192.168.0.0/16")
	if _, ok := r["range"]; !ok {
		t.Fatalf("ip CIDR 应为 range: %#v", r)
	}
}

func TestDSLNestedRejected(t *testing.T) {
	_, err := compileQuery("nested_field:abc", mkSchema())
	if err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("nested 应报 4002: %v", err)
	}
}

func TestDSLConflictLenient(t *testing.T) {
	q := mustCompile(t, "blob:hello")
	m := q["match"].(map[string]interface{})["blob"].(map[string]interface{})
	if m["lenient"] != true {
		t.Fatalf("type_conflict 应降级 match+lenient: %#v", m)
	}
}

// ---------- exists / not ----------

func TestDSLExists(t *testing.T) {
	q := mustCompile(t, "env:*")
	e, ok := q["exists"].(map[string]interface{})
	if !ok || e["field"] != "env" {
		t.Fatalf("field:* 应为 exists: %#v", q)
	}
}

func TestDSLNotWrapsMustNot(t *testing.T) {
	q := mustCompile(t, "not env:prod")
	b := getBool(t, q)
	mn, ok := b["must_not"].([]interface{})
	if !ok || len(mn) != 1 {
		t.Fatalf("NOT 应产出 must_not: %#v", b)
	}
}

func TestDSLNotEqual(t *testing.T) {
	q := mustCompile(t, "env:!=prod")
	b := getBool(t, q)
	if _, ok := b["must_not"]; !ok {
		t.Fatalf("!= 应包 must_not: %#v", b)
	}
}

// ---------- 字段不存在 / 字段表为空 ----------

func TestDSLFieldNotExist(t *testing.T) {
	_, err := compileQuery("envv:prod", mkSchema())
	if err == nil {
		t.Fatal("未知字段应报 4002")
	}
	if !strings.Contains(err.Error(), "env") {
		t.Fatalf("应给出近似字段建议: %v", err)
	}
}

func TestDSLEmptySchema(t *testing.T) {
	view := &model.ESView{ID: 1, IndexPattern: "x-*", TimeField: "@timestamp"}
	_, err := compileQuery("anything", BuildSchema(view, nil))
	if err == nil {
		t.Fatal("字段表为空应报错")
	}
}

// ---------- bool 生成红线 ----------

// 缺陷 6：任何 bool 出现 should 必带 minimum_should_match（遍历断言）。
func assertMSM(t *testing.T, v interface{}) {
	t.Helper()
	switch x := v.(type) {
	case map[string]interface{}:
		if b, ok := x["bool"].(map[string]interface{}); ok {
			if _, hasShould := b["should"]; hasShould {
				if _, hasMSM := b["minimum_should_match"]; !hasMSM {
					t.Fatalf("bool 含 should 但缺 minimum_should_match: %#v", b)
				}
			}
			for _, child := range b {
				assertMSM(t, child)
			}
		}
	case []interface{}:
		for _, item := range x {
			assertMSM(t, item)
		}
	}
}

func TestDSLShouldAlwaysHasMSM(t *testing.T) {
	cases := []string{
		"a or b", "env:prod or level:error", "env:prod and (level:error or status:>=500)",
		"not (a or b)", "env:prod and level:error or status:500",
	}
	for _, kql := range cases {
		assertMSM(t, mustCompile(t, kql))
	}
}

// 混合布尔：`a and b or c` 必须收敛进同一层 bool（must + should），且子句不丢。
func TestDSLMixedBoolSameLevel(t *testing.T) {
	q := mustCompile(t, "env:prod and level:error or status:500")
	b := getBool(t, q)
	must, _ := b["must"].([]interface{})
	should, _ := b["should"].([]interface{})
	if len(must) != 2 || len(should) != 1 {
		t.Fatalf("混合布尔应同层 must(2)+should(1): %#v", b)
	}
}

func TestDSLNilClauseForbidden(t *testing.T) {
	// 未知算子路径：q>500 → 编译期报错而非产出 must:[null]
	if _, err := compileQuery("q>500", mkSchema()); err == nil {
		t.Fatal("无字段前缀的裸算子应报错")
	}
	assertNoNil(t, mustCompile(t, "env:prod or status:500"))
}

func assertNoNil(t *testing.T, v interface{}) {
	t.Helper()
	switch x := v.(type) {
	case map[string]interface{}:
		for k, child := range x {
			if child == nil {
				t.Fatalf("发现 nil 子句（key=%s）", k)
			}
			assertNoNil(t, child)
		}
	case []interface{}:
		for _, item := range x {
			if item == nil {
				t.Fatal("发现 nil 子句")
			}
			assertNoNil(t, item)
		}
	}
}

// ---------- 搜索体组装（§10） ----------

func TestBuildSearchBodyTimeFilter(t *testing.T) {
	body, err := BuildSearchBody(mkSchema(), "env:prod", 1789117200000, 1789120800000,
		[]string{"@timestamp", "message", "host"}, "60s")
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if body["from"] != 0 || body["size"] != esResultWindow {
		t.Fatalf("窗口应恒为 0/1000: %#v", body)
	}
	if body["track_total_hits"] != true {
		t.Fatal("track_total_hits 应恒为 true")
	}
	b := body["query"].(map[string]interface{})["bool"].(map[string]interface{})
	filters := b["filter"].([]interface{})
	rf := filters[0].(map[string]interface{})["range"].(map[string]interface{})["@timestamp"].(map[string]interface{})
	if rf["gte"] != int64(1789117200000) || rf["lt"] != int64(1789120800000) {
		t.Fatalf("时间 filter 应为 gte/lt 下半开: %#v", rf)
	}
	if _, hasTZ := rf["time_zone"]; hasTZ {
		t.Fatal("时间 filter 不应下发 time_zone")
	}
	// 结束时刻文档不计入（下半开红线）
	if rf["lte"] != nil {
		t.Fatal("不得使用 lte（会把边界文档重复计入）")
	}
	// sort 首键 = 视图时间字段
	sort0 := body["sort"].([]interface{})[0].(map[string]interface{})
	if _, ok := sort0["@timestamp"]; !ok {
		t.Fatalf("sort 首键应为视图 time_field: %#v", sort0)
	}
	// _source
	src := body["_source"].([]string)
	if len(src) != 3 || src[1] != "message" {
		t.Fatalf("_source 错误: %#v", src)
	}
	// highlight 仅 text 列
	hl := body["highlight"].(map[string]interface{})["fields"].(map[string]interface{})
	if _, ok := hl["message"]; !ok {
		t.Fatalf("message 应有 highlight: %#v", hl)
	}
	if _, ok := hl["@timestamp"]; ok {
		t.Fatal("date 列不应有 highlight")
	}
	// 直方图
	dh := body["aggs"].(map[string]interface{})["histogram"].(map[string]interface{})["date_histogram"].(map[string]interface{})
	if dh["time_zone"] != "+08:00" || dh["fixed_interval"] != "60s" {
		t.Fatalf("直方图配置错误: %#v", dh)
	}
}

func TestBuildSearchBodyNoColumns(t *testing.T) {
	body, err := BuildSearchBody(mkSchema(), "", 1, 2, nil, "")
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if _, ok := body["_source"]; ok {
		t.Fatal("无列选择不应下发 _source")
	}
	if _, ok := body["highlight"]; ok {
		t.Fatal("无列选择不应下发 highlight")
	}
	if _, ok := body["aggs"]; ok {
		t.Fatal("无 interval 不应下发直方图")
	}
}

// 缺陷 4：裸值 multi_match.fields 必须来自字段表真实候选 + lenient。
func TestDSLBareMultiMatchFields(t *testing.T) {
	q := mustCompile(t, "nginx")
	mm := q["multi_match"].(map[string]interface{})
	fields := mm["fields"].([]string)
	if len(fields) == 0 {
		t.Fatal("fields 不得为空")
	}
	for _, f := range fields {
		if f == "*" {
			t.Fatal("禁止 [\"*\"]")
		}
	}
	if mm["lenient"] != true {
		t.Fatal("multi_match 应带 lenient:true")
	}
	// 引号裸值 → phrase
	q2 := mustCompile(t, `"failed to connect"`)
	mm2 := q2["multi_match"].(map[string]interface{})
	if mm2["type"] != "phrase" {
		t.Fatalf("引号裸值应为 phrase: %#v", mm2)
	}
}

// ---------- B9 红线（§4 缺陷 3 / 5） ----------

// 缺陷 3：值为单个引号 → 报 4001，绝不越界 panic。
func TestDSLSingleQuoteValueNoPanic(t *testing.T) {
	for _, kql := range []string{`msg:"`, `msg:'`, `msg:""`, `"`, `'`} {
		if _, err := compileQuery(kql, mkSchema()); err == nil {
			t.Errorf("输入 %q 应报错", kql)
		}
	}
}

// 缺陷 5：单遍解转义，\\" 不是转义引号 —— 字符串在 a\ 后终止，余下内容报错而非静默拼接。
func TestDSLNoDoubleUnescape(t *testing.T) {
	if _, err := compileQuery(`message:"a\\"b"`, mkSchema()); err == nil {
		t.Fatal(`\\" 后的引号应终止字符串并因残留内容报错（单遍解转义）`)
	}
	// 合法转义引号：\" → 字面引号
	q := mustCompile(t, `message:"a\"b"`)
	mp := q["match_phrase"].(map[string]interface{})["message"]
	if mp != `a"b` {
		t.Fatalf(`\" 应解为字面引号: %#v`, mp)
	}
}
