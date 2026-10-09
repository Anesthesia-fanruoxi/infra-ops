package mysql

import (
	"strings"
	"testing"

	"infra-ops/model"
)

// TestExplainRefs 表引用解析：别名、库前缀、反引号、关键字守卫、UPDATE / INTO。
func TestExplainRefs(t *testing.T) {
	byAlias, plain := explainRefs("SELECT * FROM demo.users u JOIN orders AS o ON o.user_id = u.id WHERE u.status = 1")
	if r := byAlias["u"]; r.Schema != "demo" || r.Table != "users" || r.Alias != "u" {
		t.Fatalf("别名 u 解析不对: %+v", r)
	}
	if r := byAlias["o"]; r.Schema != "" || r.Table != "orders" || r.Alias != "o" {
		t.Fatalf("AS 别名 o 解析不对: %+v", r)
	}
	if _, ok := plain["users"]; !ok {
		t.Fatalf("users 原名映射缺失: %+v", plain)
	}

	// 关键字跟在表后不是别名，也不能吞掉下一张表的 JOIN
	_, p2 := explainRefs("SELECT * FROM users WHERE id = 1")
	if len(p2) != 1 {
		t.Fatalf("关键字守卫失败: %+v", p2)
	}
	for _, r := range p2 {
		if r.Alias != "" {
			t.Fatalf("WHERE 被当成别名: %+v", r)
		}
	}

	// 反引号库.表 + 反引号别名
	b3, _ := explainRefs("SELECT * FROM `demo`.`users` `u`")
	if r := b3["u"]; r.Schema != "demo" || r.Table != "users" {
		t.Fatalf("反引号解析不对: %+v", r)
	}

	// UPDATE / INSERT INTO 同样收敛
	b4, _ := explainRefs("UPDATE demo.t x SET a = 1")
	if r := b4["x"]; r.Schema != "demo" || r.Table != "t" {
		t.Fatalf("UPDATE 别名解析不对: %+v", r)
	}
	_, p5 := explainRefs("INSERT INTO demo.t VALUES (1)")
	if _, ok := p5["t"]; !ok {
		t.Fatalf("INTO 表解析缺失: %+v", p5)
	}
}

// TestExplainPlanTables 计划取表：传统格式看 table 列、JSON 格式抽 table_name、伪表跳过。
func TestExplainPlanTables(t *testing.T) {
	tr := &model.MySQLQueryResult{
		Columns: []model.MySQLResultColumn{{Name: "id"}, {Name: "table"}},
		Rows: [][]interface{}{
			{1, []byte("u")},
			{1, []byte("<derived2>")},
			{nil, "orders"},
		},
	}
	got := explainPlanTables(tr)
	if len(got) != 2 || got[0] != "u" || got[1] != "orders" {
		t.Fatalf("traditional 取表 = %v，期望 [u orders]", got)
	}

	js := &model.MySQLQueryResult{
		Columns: []model.MySQLResultColumn{{Name: "EXPLAIN"}},
		Rows: [][]interface{}{{
			`{"query_block":{"table":{"table_name":"users","access_type":"ALL"},"nested_loop":[{"table":{"table_name":"orders"}}]}}`,
		}},
	}
	got2 := explainPlanTables(js)
	if len(got2) != 2 || got2[0] != "users" || got2[1] != "orders" {
		t.Fatalf("JSON 取表 = %v，期望 [users orders]", got2)
	}
}

// TestExplainResolve 计划表名 → 真实表：别名优先、原名兜底、默认库填充、去重与伪表过滤。
func TestExplainResolve(t *testing.T) {
	byAlias, plain := explainRefs("SELECT * FROM demo.users u JOIN orders o ON o.user_id = u.id")
	refs := explainResolve([]string{"u", "orders", "some_view", "<ignored>", "users"}, byAlias, plain, "demo")
	if len(refs) != 3 {
		t.Fatalf("解析数量 = %d: %+v", len(refs), refs)
	}
	if refs[0].Table != "users" || refs[0].Schema != "demo" {
		t.Fatalf("别名回映不对: %+v", refs[0])
	}
	if refs[1].Table != "orders" || refs[1].Schema != "demo" {
		t.Fatalf("原名解析不对: %+v", refs[1])
	}
	if refs[2].Table != "some_view" || refs[2].Schema != "demo" {
		t.Fatalf("兜底解析不对: %+v", refs[2])
	}
}

// TestExplainTablesText 文本拼装：表头含统计、列与索引逐行、说明句与空列表分支。
func TestExplainTablesText(t *testing.T) {
	if s := explainTablesText(nil); !strings.Contains(s, "未从执行计划") {
		t.Fatalf("空表说明缺失: %q", s)
	}
	text := explainTablesText([]model.MySQLExplainTable{{
		Schema: "demo", Name: "users", Engine: "InnoDB", Rows: 10240, DataLength: 1024, IndexLength: 2048,
		Columns: []string{"id bigint NOT NULL PRI 主键"},
		Indexes: []string{"PRIMARY UNIQUE (id) 基数≈10240"},
		Note:    "列过多，仅列前 40 列（共 60 列）",
	}})
	for _, sub := range []string{"表 demo.users", "InnoDB", "估算行数 10240", "列 id bigint NOT NULL PRI 主键", "索引 PRIMARY UNIQUE (id) 基数≈10240", "注："} {
		if !strings.Contains(text, sub) {
			t.Fatalf("缺 %q:\n%s", sub, text)
		}
	}
}
