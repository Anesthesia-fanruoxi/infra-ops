package mysql

import (
	"strings"
	"testing"
	"time"

	"infra-ops/model"
)

// modelConnRO / modelConnRW 默认模式测试用的两种连接配置。
var (
	modelConnRO = model.MySQLConn{ReadOnly: true}
	modelConnRW = model.MySQLConn{ReadOnly: false}
)

// TestClassifySQL 门禁「判断 2」的判据：单条语句必须落到正确的 DQL / SESSION / DML / DDL / OTHER。
func TestClassifySQL(t *testing.T) {
	cases := []struct {
		name     string
		sql      string
		wantKind string
		wantType string
	}{
		{"普通查询", "SELECT 1", "SELECT", sqlDQL},
		{"大小写混合与空白", "  \n select * from t ", "SELECT", sqlDQL},
		{"行注释（--）", "-- 注释\nSELECT 1", "SELECT", sqlDQL},
		{"行注释（#）", "# 注释\nselect 1", "SELECT", sqlDQL},
		{"块注释", "/* c */ SHOW TABLES", "SHOW", sqlDQL},
		{"多行块注释", "/*\n c\n*/SHOW TABLES", "SHOW", sqlDQL},
		{"描述表", "DESC users", "DESC", sqlDQL},
		{"执行计划", "EXPLAIN SELECT 1", "EXPLAIN", sqlDQL},
		{"CTE 查询", "WITH x AS (SELECT 1) SELECT * FROM x", "WITH", sqlDQL},
		{"CTE 写操作（关键用例）", "WITH x AS (SELECT 1) DELETE FROM t", "DELETE", sqlDML},
		{"插入", "INSERT INTO t VALUES(1)", "INSERT", sqlDML},
		{"更新", "UPDATE t SET a=1", "UPDATE", sqlDML},
		{"小写删除", "delete from t", "DELETE", sqlDML},
		{"替换", "REPLACE INTO t VALUES(1)", "REPLACE", sqlDML},
		{"清表", "TRUNCATE TABLE t", "TRUNCATE", sqlDDL},
		{"删表", "DROP TABLE t", "DROP", sqlDDL},
		{"变更表结构", "ALTER TABLE t ADD COLUMN c INT", "ALTER", sqlDDL},
		{"建表", "CREATE TABLE t(a INT)", "CREATE", sqlDDL},
		{"重命名", "RENAME TABLE a TO b", "RENAME", sqlDDL},
		{"会话变量", "SET SESSION sql_mode=''", "SET", sqlSession},
		{"切库", "USE demo", "USE", sqlSession},
		{"事务开始", "BEGIN", "BEGIN", sqlSession},
		{"提交", "COMMIT", "COMMIT", sqlSession},
		{"存储过程", "CALL p()", "CALL", sqlOther},
		{"赋权", "GRANT SELECT ON *.* TO u", "GRANT", sqlOther},
		{"空语句", "   \n  ", "", sqlOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, sqlType := classifySQL(tc.sql)
			if kind != tc.wantKind || sqlType != tc.wantType {
				t.Fatalf("classifySQL(%q) = (%q, %q)，期望 (%q, %q)", tc.sql, kind, sqlType, tc.wantKind, tc.wantType)
			}
		})
	}
}

// TestSplitStatements 分号切分：字符串 / 反引号 / 注释里的分号都不能当分隔符。
func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"单条", "SELECT 1", []string{"SELECT 1"}},
		{"两条", "SELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"结尾分号", "SELECT 1;", []string{"SELECT 1"}},
		{"连续分号与空白", "SELECT 1 ;; ; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"字符串内的分号", "SELECT ';'", []string{"SELECT ';'"}},
		{"转义引号内的分号", `SELECT 'a\';b'`, []string{`SELECT 'a\';b'`}},
		{"反引号内的分号", "SELECT `a;b` FROM t", []string{"SELECT `a;b` FROM t"}},
		{"行注释内的分号", "SELECT 1 -- 注释; 还有\n", []string{"SELECT 1"}},
		{"块注释内的分号", "SELECT /* ; */ 1", []string{"SELECT 1"}},
		{"空输入", "   ", nil},
		{"纯注释", "-- 只有注释", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitStatements(tc.sql)
			if len(got) != len(tc.want) {
				t.Fatalf("splitStatements(%q) 得到 %d 条 %v，期望 %d 条 %v", tc.sql, len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if normWS(got[i]) != normWS(tc.want[i]) {
					t.Fatalf("第 %d 条 = %q，期望 %q", i+1, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestAnalyzeSQL 整段判定：多语句必须取风险最高的一条，不能让首关键词蒙混过关。
func TestAnalyzeSQL(t *testing.T) {
	cases := []struct {
		name        string
		sql         string
		wantType    string
		wantKind    string
		wantCount   int
		wantConfirm bool
	}{
		{"单条查询", "SELECT 1", sqlDQL, "SELECT", 1, false},
		{"会话控制免确认", "SET NAMES utf8mb4", sqlSession, "SET", 1, false},
		{"单条更新需确认", "UPDATE t SET a=1", sqlDML, "UPDATE", 1, true},
		{"多语句查询", "SELECT 1; SELECT 2", sqlDQL, "SELECT", 2, false},
		{"多语句绕过（关键用例）", "SELECT 1; DROP TABLE t", sqlDDL, "DROP", 2, true},
		{"多语句取最高风险", "UPDATE t SET a=1; DROP TABLE t", sqlDDL, "DROP", 2, true},
		{"DQL 与 DML 混合", "SELECT 1; DELETE FROM t", sqlDML, "DELETE", 2, true},
		{"DQL 与会话控制混合", "USE demo; SELECT 1", sqlSession, "USE", 2, false},
		{"未知语句按最严", "CALL p()", sqlOther, "CALL", 1, true},
		{"无首关键词怪语句", "0", sqlOther, "", 1, true},
		{"怪语句与查询混排", "SELECT 1; 0", sqlOther, "", 2, true},
		{"怪语句在最前", "0; SELECT 1", sqlOther, "", 2, true},
		{"空输入保守处理", "  ", sqlOther, "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, an := analyzeSQL(tc.sql)
			if an.Type != tc.wantType || an.Kind != tc.wantKind {
				t.Fatalf("analyzeSQL(%q) 类型 = (%q, %q)，期望 (%q, %q)", tc.sql, an.Kind, an.Type, tc.wantKind, tc.wantType)
			}
			if len(an.Statements) != tc.wantCount {
				t.Fatalf("analyzeSQL(%q) 语句数 = %d，期望 %d", tc.sql, len(an.Statements), tc.wantCount)
			}
			if an.RequireConfirm() != tc.wantConfirm {
				t.Fatalf("analyzeSQL(%q) RequireConfirm = %v，期望 %v", tc.sql, an.RequireConfirm(), tc.wantConfirm)
			}
		})
	}
}

// TestConfirmLevel 确认分级：只读模式下的放行始终是最高级警示。
func TestConfirmLevel(t *testing.T) {
	cases := []struct {
		mode    string
		sqlType string
		want    string
	}{
		{ModeRead, sqlDML, "readonly"},
		{ModeRead, sqlDDL, "readonly"},
		{ModeRead, sqlOther, "readonly"},
		{ModeWrite, sqlDML, "write"},
		{ModeWrite, sqlDDL, "ddl"},
		{ModeWrite, sqlOther, "ddl"},
	}
	for _, tc := range cases {
		if got := confirmLevel(tc.mode, tc.sqlType); got != tc.want {
			t.Fatalf("confirmLevel(%q, %q) = %q，期望 %q", tc.mode, tc.sqlType, got, tc.want)
		}
	}
}

// TestDefaultMode 连接的默认模式由 read_only 决定。
func TestDefaultMode(t *testing.T) {
	if got := defaultMode(&modelConnRO); got != ModeRead {
		t.Fatalf("只读连接默认模式应为 read，实际 %q", got)
	}
	if got := defaultMode(&modelConnRW); got != ModeWrite {
		t.Fatalf("可写连接默认模式应为 write，实际 %q", got)
	}
}

// TestModeRegistry 模式状态：首次读取用默认值初始化，设置后以设置为准，删除后回落默认值。
func TestModeRegistry(t *testing.T) {
	r := newModeRegistry()

	if got := r.get(7, ModeRead); got != ModeRead {
		t.Fatalf("未设置过应返回默认模式，实际 %q", got)
	}
	r.set(7, ModeWrite)
	if got := r.get(7, ModeRead); got != ModeWrite {
		t.Fatalf("设置后应返回已设置的模式，实际 %q", got)
	}
	// 已设置过的连接不再被默认值覆盖（这正是不接受硬门禁的语义：连上之后由使用者说了算）
	if got := r.get(7, ModeRead); got != ModeWrite {
		t.Fatalf("已有模式不应被默认值覆盖，实际 %q", got)
	}
	r.drop(7)
	if got := r.get(7, ModeRead); got != ModeRead {
		t.Fatalf("删除后应回落到默认模式，实际 %q", got)
	}
}

// TestNormalizeValue 结果值 JSON 化：字节转字符串、时间转本地格式，NULL 保持 nil。
func TestNormalizeValue(t *testing.T) {
	if got := normalizeValue(nil); got != nil {
		t.Fatalf("NULL 应保持 nil，实际 %#v", got)
	}
	if got := normalizeValue([]byte("abc")); got != "abc" {
		t.Fatalf("[]byte 应转 string，实际 %#v", got)
	}
	if got := normalizeValue(int64(7)); got != int64(7) {
		t.Fatalf("int64 应原样保留，实际 %#v", got)
	}
	ts := time.Date(2026, 10, 8, 9, 5, 3, 0, time.Local)
	if got := normalizeValue(ts); got != "2026-10-08 09:05:03" {
		t.Fatalf("时间格式化错误，实际 %#v", got)
	}
}

// TestQueryTimeout 超时归一化：默认 30s，上限 300s。
func TestQueryTimeout(t *testing.T) {
	if got := queryTimeout(0); got != defaultQueryTimeout {
		t.Fatalf("未指定应取默认值，实际 %v", got)
	}
	if got := queryTimeout(5 * time.Second); got != 5*time.Second {
		t.Fatalf("合法值应原样返回，实际 %v", got)
	}
	if got := queryTimeout(time.Hour); got != maxQueryTimeout {
		t.Fatalf("超上限应被夹取，实际 %v", got)
	}
}

// normWS 压掉连续空白，避免断言被注释替换产生的空格数影响。
func normWS(s string) string { return strings.Join(strings.Fields(s), " ") }
