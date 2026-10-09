package mysql

import (
	"testing"

	"infra-ops/model"
)

// TestExplainTarget 边界判定：只有 EXPLAIN 后跟「可执行计划的目标语句」才算执行计划。
func TestExplainTarget(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"EXPLAIN SELECT 1", true},
		{"explain select * from t", true},
		{"EXPLAIN ANALYZE SELECT * FROM t", true},
		{"EXPLAIN EXTENDED SELECT 1", true},
		{"EXPLAIN PARTITIONS SELECT 1", true},
		{"EXPLAIN FORMAT=JSON SELECT 1", true},
		{"EXPLAIN FORMAT = TREE SELECT 1", true},
		{"EXPLAIN FORMAT=TRADITIONAL SELECT 1", true},
		{"EXPLAIN UPDATE t SET a = 1", true},
		{"EXPLAIN DELETE FROM t", true},
		{"EXPLAIN INSERT INTO t VALUES (1)", true},
		{"EXPLAIN REPLACE INTO t VALUES (1)", true},
		{"EXPLAIN WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"/* c */ EXPLAIN SELECT 1", true},
		// 表结构（等价 DESC）不是执行计划
		{"EXPLAIN users", false},
		{"EXPLAIN `demo`.`users`", false},
		{"EXPLAIN `select`", false}, // 表名恰为关键字：剥反引号后不得当成目标语句
		{"EXPLAIN users name", false},
		// FOR CONNECTION 无法重跑复现，不支持
		{"EXPLAIN FOR CONNECTION 12", false},
		// 非 EXPLAIN 语句
		{"SELECT 1", false},
		{"DESC users", false},
		{"DESCRIBE users", false},
		{"DELETE FROM t", false},
		{"", false},
		{"EXPLAIN", false},
		{"EXPLAIN ANALYZE", false},
	}
	for _, tc := range cases {
		if got := explainTarget(tc.sql); got != tc.want {
			t.Fatalf("explainTarget(%q) = %v，期望 %v", tc.sql, got, tc.want)
		}
	}
}

// TestExplainCanAnalyze 组合判定：单条 EXPLAIN 执行计划才可分析，多语句混合一律拒绝。
func TestExplainCanAnalyze(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"EXPLAIN SELECT 1", true},
		{"EXPLAIN FORMAT=JSON SELECT * FROM demo.orders WHERE id = 1", true},
		{"EXPLAIN users", false},
		{"SELECT 1", false},
		{"SELECT 1; EXPLAIN SELECT 2", false}, // 多语句混 EXPLAIN：分析对象不唯一，拒绝
		{"EXPLAIN SELECT 1; SELECT 2", false},
		{"", false},
	}
	for _, tc := range cases {
		stmts, an := analyzeSQL(tc.sql)
		if got := explainCanAnalyze(stmts, an); got != tc.want {
			t.Fatalf("explainCanAnalyze(%q) = %v，期望 %v", tc.sql, got, tc.want)
		}
	}
}

// TestBuildExplainDigest 计划摘要：列名=值 拼接、NULL 显式标注、行数超限截断。
func TestBuildExplainDigest(t *testing.T) {
	res := &model.MySQLQueryResult{
		Columns: []model.MySQLResultColumn{{Name: "id"}, {Name: "table"}, {Name: "Extra"}},
		Rows: [][]interface{}{
			{1, []byte("orders"), nil},
			{"1", "users", "Using filesort"},
		},
	}
	d := buildExplainDigest(res)
	if d.RowCount != 2 || d.Truncated || len(d.Lines) != 2 {
		t.Fatalf("digest 基础字段不对: %+v", d)
	}
	if d.Lines[0] != "id=1 table=orders Extra=NULL" {
		t.Fatalf("首行 = %q", d.Lines[0])
	}

	rows := make([][]interface{}, explainDigestMaxRows+1)
	for i := range rows {
		rows[i] = []interface{}{i}
	}
	d2 := buildExplainDigest(&model.MySQLQueryResult{Columns: []model.MySQLResultColumn{{Name: "id"}}, Rows: rows})
	if !d2.Truncated || len(d2.Lines) != explainDigestMaxRows {
		t.Fatalf("行数截断不对: truncated=%v lines=%d", d2.Truncated, len(d2.Lines))
	}
}

// TestExplainIndexList 建索引语句清洗：去空白、丢空项、封顶 3 条；空输入返回空切片（JSON 输出 [] 而非 null）。
func TestExplainIndexList(t *testing.T) {
	got := explainIndexList([]string{"  ALTER TABLE `demo`.`users` ADD INDEX `idx_a` (`a`);  ", "", "   "})
	if len(got) != 1 || got[0] != "ALTER TABLE `demo`.`users` ADD INDEX `idx_a` (`a`);" {
		t.Fatalf("清洗结果 = %+v", got)
	}
	if got := explainIndexList([]string{"s1", "s2", "s3", "s4", "s5"}); len(got) != explainMaxIndexDDL {
		t.Fatalf("封顶失败: %v", got)
	}
	if got := explainIndexList(nil); got == nil || len(got) != 0 {
		t.Fatalf("空输入应为空切片: %#v", got)
	}
}

// TestExplainSameSQL 等价判定：空白 / 大小写 / 末尾分号归一后再比，用于丢弃「原样抄回」的优化后语句。
func TestExplainSameSQL(t *testing.T) {
	if !explainSameSQL("EXPLAIN  SELECT\n\t1;", "explain select 1") {
		t.Fatal("空白与大小写应归一后相等")
	}
	if explainSameSQL("EXPLAIN SELECT 1", "EXPLAIN SELECT 2") {
		t.Fatal("语义不同不得判等")
	}
	if explainSameSQL("", "EXPLAIN SELECT 1") {
		t.Fatal("空串与语句不得判等")
	}
}
