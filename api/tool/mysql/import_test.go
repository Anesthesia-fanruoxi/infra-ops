package mysql

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"infra-ops/model"
)

// ---------------- 假执行面：直接验分批编排，不依赖真库 ----------------

type fakeResult struct{ affected int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.affected, nil }

type fakeRunner struct {
	failOn    string   // 语句含此子串则报错
	executed  []string // 落在事务里的语句
	direct    []string // 事务外直接执行的语句（DDL 走这条）
	commits   int
	rollbacks int
}

func (r *fakeRunner) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	if r.failOn != "" && strings.Contains(q, r.failOn) {
		return nil, errors.New("boom")
	}
	r.direct = append(r.direct, q)
	return fakeResult{affected: 1}, nil
}

func (r *fakeRunner) begin(context.Context) (importTx, error) { return &fakeTx{runner: r}, nil }

type fakeTx struct{ runner *fakeRunner }

func (t *fakeTx) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	if t.runner.failOn != "" && strings.Contains(q, t.runner.failOn) {
		return nil, errors.New("boom")
	}
	t.runner.executed = append(t.runner.executed, q)
	return fakeResult{affected: 1}, nil
}

func (t *fakeTx) Commit() error   { t.runner.commits++; return nil }
func (t *fakeTx) Rollback() error { t.runner.rollbacks++; return nil }

func newImportJob(batch int, onError string, allowSchema bool) *importJob {
	return &importJob{batchSize: batch, onError: onError, allowSchema: allowSchema}
}

// TestImportInBatches 分批边界：5 条语句、每批 2 条 → 3 个事务，最后一批 1 条也要提交。
func TestImportInBatches(t *testing.T) {
	r := &fakeRunner{}
	res := &importResult{Errors: []string{}}
	src := strings.NewReader("INSERT INTO t VALUES (1);INSERT INTO t VALUES (2);" +
		"INSERT INTO t VALUES (3);INSERT INTO t VALUES (4);INSERT INTO t VALUES (5);")
	h := &Handler{}
	if err := h.importInBatches(context.Background(), r, newSQLScanner(src, 0), newImportJob(2, "stop", false), res); err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if r.commits != 3 || r.rollbacks != 0 {
		t.Fatalf("应提交 3 个事务且无回滚，实际 commit=%d rollback=%d", r.commits, r.rollbacks)
	}
	if res.Statements != 5 || res.Affected != 5 || res.Batches != 3 {
		t.Fatalf("统计不符：statements=%d affected=%d batches=%d", res.Statements, res.Affected, res.Batches)
	}
}

// TestImportBatchRollbackStopsAtFailure 批内失败：整批回滚、停在失败处、
// 报错里要指明是第几条（否则几千条里根本找不到是哪句坏了）。
func TestImportBatchRollbackStopsAtFailure(t *testing.T) {
	r := &fakeRunner{failOn: "bad"}
	res := &importResult{Errors: []string{}}
	src := strings.NewReader("INSERT INTO t VALUES (1);INSERT INTO t VALUES (2);" +
		"INSERT INTO bad VALUES (3);INSERT INTO t VALUES (4);")
	h := &Handler{}
	err := h.importInBatches(context.Background(), r, newSQLScanner(src, 0), newImportJob(10, "stop", false), res)
	if err == nil {
		t.Fatal("失败语句应当让导入中止")
	}
	if r.rollbacks != 1 {
		t.Fatalf("本批应当回滚，实际 rollback=%d", r.rollbacks)
	}
	if r.commits != 0 {
		t.Fatalf("失败批不应提交，实际 commit=%d", r.commits)
	}
	for _, want := range []string{"第 3 条", "本批共 4 条，已整体回滚", "INSERT INTO bad"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错缺少 %q：%v", want, err)
		}
	}
}

// TestImportSkipModeContinues 跳过策略：坏语句记账后继续，其余照常执行统计。
func TestImportSkipModeContinues(t *testing.T) {
	r := &fakeRunner{failOn: "bad"}
	res := &importResult{Errors: []string{}}
	src := strings.NewReader("INSERT INTO t VALUES (1);INSERT INTO bad VALUES (2);" +
		"INSERT INTO t VALUES (3);")
	h := &Handler{}
	if err := h.importStatementByStatement(context.Background(), r, newSQLScanner(src, 0), newImportJob(10, "skip", false), res); err != nil {
		t.Fatalf("跳过模式不应中止: %v", err)
	}
	if res.Statements != 2 || res.Skipped != 1 {
		t.Fatalf("应成功 2 条、跳过 1 条，实际 %d / %d", res.Statements, res.Skipped)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "第 2 条") {
		t.Fatalf("失败明细应记下第 2 条，实际 %v", res.Errors)
	}
}

// TestImportDDLOutsideBatch DDL 会隐式提交事务，必须先把手里的批落掉再单独执行。
func TestImportDDLOutsideBatch(t *testing.T) {
	r := &fakeRunner{}
	res := &importResult{Errors: []string{}}
	src := strings.NewReader("INSERT INTO t VALUES (1);CREATE TABLE t2 (id INT);INSERT INTO t2 VALUES (2);")
	h := &Handler{}
	if err := h.importInBatches(context.Background(), r, newSQLScanner(src, 0), newImportJob(100, "stop", true), res); err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(r.direct) != 1 || !strings.HasPrefix(r.direct[0], "CREATE TABLE") {
		t.Fatalf("DDL 应走事务外直接执行，实际 %v", r.direct)
	}
	// DDL 前后各有一个批事务
	if r.commits != 2 {
		t.Fatalf("DDL 应把批切成两段提交，实际 commit=%d", r.commits)
	}
	if res.Statements != 3 {
		t.Fatalf("共 3 条成功，实际 %d", res.Statements)
	}
}

// TestCheckImportStatement 结构变更与未知语句默认不放行。
func TestCheckImportStatement(t *testing.T) {
	pass := [][2]string{
		{"SELECT", model.SQLTypeDQL},
		{"SET", model.SQLTypeSession},
		{"INSERT", model.SQLTypeDML},
		{"UPDATE", model.SQLTypeDML},
		{"DELETE", model.SQLTypeDML},
	}
	for _, c := range pass {
		if err := checkImportStatement(c[0], c[1], 1, "stmt", false); err != nil {
			t.Errorf("%s 应当放行: %v", c[0], err)
		}
	}
	if err := checkImportStatement("DROP", model.SQLTypeDDL, 7, "DROP TABLE users", false); err == nil {
		t.Error("DDL 默认应当拒绝")
	} else if !strings.Contains(err.Error(), "第 7 条") || !strings.Contains(err.Error(), "允许结构变更") {
		t.Errorf("拒绝信息应指明条数并给出放行方式: %v", err)
	}
	if err := checkImportStatement("DROP", model.SQLTypeDDL, 7, "DROP TABLE users", true); err != nil {
		t.Errorf("开了开关应当放行: %v", err)
	}
	if err := checkImportStatement("CALL", model.SQLTypeOther, 3, "CALL p()", false); err == nil {
		t.Error("无法归类的语句应当拒绝")
	}
}

func TestNormalizeImportSource(t *testing.T) {
	if _, err := normalizeImportSource(""); err == nil {
		t.Error("空路径应当报错")
	}
	if _, err := normalizeImportSource("a.sql"); err == nil {
		t.Error("相对路径应当报错")
	}
	if _, err := normalizeImportSource(filepath.Join(t.TempDir(), "nope.sql")); err == nil {
		t.Error("文件不存在应当报错")
	}
	if _, err := normalizeImportSource(t.TempDir()); err == nil {
		t.Error("目录应当报错")
	}
}
