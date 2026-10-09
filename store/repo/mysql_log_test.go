package repo

import (
	"path/filepath"
	"testing"

	"infra-ops/model"
	"infra-ops/store"
)

// setupLogDB 每个用例一个独立的临时库：store.DB 是包级全局，用例之间必须互不干扰。
func setupLogDB(t *testing.T) {
	t.Helper()
	if err := store.Open(filepath.Join(t.TempDir(), "log.db")); err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(store.Close)
}

// seedSQLLogs 造 2 条 SQL 执行记录（1 查询 1 写入，同一连接）。
func seedSQLLogs(t *testing.T, r *MySQLLogRepo) {
	t.Helper()
	sqlRows := []model.MySQLSQLLog{
		{ConnID: 1, ConnName: "cs", SchemaName: "demo", SQL: "SELECT * FROM t_user LIMIT 200",
			StatementCount: 1, SQLType: model.SQLTypeDQL, StatementKind: "SELECT",
			Mode: "read", RowCount: 42, ElapsedMs: 12, Status: model.LogStatusOK},
		{ConnID: 1, ConnName: "cs", SchemaName: "demo", SQL: "UPDATE t_user SET a=1",
			StatementCount: 1, SQLType: model.SQLTypeDML, StatementKind: "UPDATE",
			Mode: "write", Confirmed: true, AffectedRows: 3, ElapsedMs: 20, Status: model.LogStatusOK},
	}
	for i := range sqlRows {
		if _, err := r.AddSQLLog(&sqlRows[i]); err != nil {
			t.Fatalf("写 SQL 记录失败: %v", err)
		}
	}
}

func TestMySQLLogListAndSummarize(t *testing.T) {
	setupLogDB(t)
	r := NewMySQLLogRepo()
	seedSQLLogs(t, r)

	// SQL 执行记录：列表按新 → 旧
	sqlList, sqlTotal, err := r.ListSQLLogs(model.MySQLLogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("列 SQL 记录失败: %v", err)
	}
	if sqlTotal != 2 || len(sqlList) != 2 {
		t.Fatalf("SQL 记录 = %d 条 / total=%d，期望 2/2", len(sqlList), sqlTotal)
	}
	if !sqlList[0].Confirmed {
		t.Errorf("confirmed 应从 0/1 正确还原：%+v", sqlList[0])
	}
	ssum, err := r.SummarizeSQLLogs(model.MySQLLogFilter{})
	if err != nil {
		t.Fatalf("汇总 SQL 记录失败: %v", err)
	}
	if ssum.Runs != 2 || ssum.OK != 2 || ssum.Failed != 0 {
		t.Errorf("汇总计数不对：runs=%d ok=%d failed=%d", ssum.Runs, ssum.OK, ssum.Failed)
	}
	if ssum.QueryRuns != 1 || ssum.WriteRuns != 1 {
		t.Errorf("查询/写入条数不对：%d/%d", ssum.QueryRuns, ssum.WriteRuns)
	}
	if ssum.RowsReturned != 42 || ssum.RowsAffected != 3 {
		t.Errorf("行数汇总不对：返回 %d 影响 %d", ssum.RowsReturned, ssum.RowsAffected)
	}
	if dql, _, _ := r.ListSQLLogs(model.MySQLLogFilter{SQLType: model.SQLTypeDQL, Page: 1, PageSize: 10}); len(dql) != 1 || dql[0].StatementKind != "SELECT" {
		t.Errorf("按语句类型过滤结果不符：%+v", dql)
	}
	if s, _ := r.SummarizeSQLLogs(model.MySQLLogFilter{ConnID: 1}); s.Runs != 2 {
		t.Errorf("按连接过滤后 runs=%d，期望 2", s.Runs)
	}
}

func TestMySQLLogClearAndPurge(t *testing.T) {
	setupLogDB(t)
	r := NewMySQLLogRepo()
	seedSQLLogs(t, r)

	// 造一条 40 天前的记录，保留期 30 天时它应被清掉、其余不动
	old, err := r.AddSQLLog(&model.MySQLSQLLog{ConnID: 9, ConnName: "old",
		SQLType: model.SQLTypeDQL, Status: model.LogStatusOK})
	if err != nil {
		t.Fatalf("写旧记录失败: %v", err)
	}
	if _, err := store.DB.Exec(
		`UPDATE mysql_sql_logs SET created_at = datetime('now','localtime','-40 days') WHERE id = ?`, old); err != nil {
		t.Fatalf("改记录时间失败: %v", err)
	}
	n, err := r.PurgeBefore(30)
	if err != nil {
		t.Fatalf("按保留期清理失败: %v", err)
	}
	if n != 1 {
		t.Errorf("应清理 1 条 SQL 记录，实际 %d", n)
	}
	// 保留期非正数时不清理任何数据
	if m, err := r.PurgeBefore(0); err != nil || m != 0 {
		t.Errorf("保留期为 0 时不应清理：%d err=%v", m, err)
	}

	// 按连接清空只影响该连接（连接 9 的旧记录已被清理，连接 1 还剩 2 条）
	deleted, err := r.ClearLogs(1)
	if err != nil {
		t.Fatalf("按连接清空失败: %v", err)
	}
	if deleted != 2 {
		t.Errorf("应删 2 条 SQL 记录，实际 %d", deleted)
	}
	if _, total, _ := r.ListSQLLogs(model.MySQLLogFilter{Page: 1, PageSize: 10}); total != 0 {
		t.Errorf("SQL 记录应已清空，剩余 %d", total)
	}
}
