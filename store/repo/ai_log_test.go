package repo

import (
	"path/filepath"
	"testing"

	"infra-ops/model"
	"infra-ops/store"
)

// setupAILogDB 每个用例一个独立的临时库（store.DB 是包级全局，用例之间必须互不干扰）。
func setupAILogDB(t *testing.T) {
	t.Helper()
	if err := store.Open(filepath.Join(t.TempDir(), "ai-log.db")); err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(store.Close)
}

// seedAILogs 造 4 条记录：mysql 3 条（2 成功 1 失败，其中 1 条用量为估算）+ metrics 1 条。
func seedAILogs(t *testing.T, r *AILogRepo) {
	t.Helper()
	rows := []model.AILog{
		{Menu: model.AIMenuMySQL, ConnID: 1, ConnName: "cs", SchemaName: "demo", Kind: model.AILogKindSQL, Mode: "read",
			Model: "mimo", BaseURL: "https://x/v1", TableCount: 3, Question: "最近 7 天订单",
			Expr: "SELECT 1", PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
			TokenSource: model.TokenSourceAPI, ElapsedMs: 900, Status: model.LogStatusOK},
		{Menu: model.AIMenuMySQL, ConnID: 1, ConnName: "cs", SchemaName: "demo", Kind: model.AILogKindSQL, Mode: "read",
			Model: "mimo", TableCount: 2, Question: "再来一句", Expr: "SELECT 2",
			PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60,
			TokenSource: model.TokenSourceEstimated, ElapsedMs: 700, Status: model.LogStatusOK},
		{Menu: model.AIMenuMySQL, ConnID: 2, ConnName: "other", SchemaName: "shop", Kind: model.AILogKindCatalog,
			BatchNo: 1, BatchTotal: 2, TableCount: 4, Model: "mimo",
			Status: model.LogStatusError, Error: "AI 服务返回 500", ElapsedMs: 300},
		{Menu: model.AIMenuMetrics, ConnID: 5, ConnName: "vm", Kind: model.MetricsAIKindPromQL,
			Model: "mimo", MetricCount: 120, Question: "看下某容器内存", Expr: `container_memory_usage{container="jxh-app"}`,
			PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230,
			TokenSource: model.TokenSourceAPI, ElapsedMs: 1500, Status: model.LogStatusOK},
	}
	for i := range rows {
		if _, err := r.Add(&rows[i]); err != nil {
			t.Fatalf("写 AI 记录失败: %v", err)
		}
	}
}

func TestAILogListAndSummarize(t *testing.T) {
	setupAILogDB(t)
	r := NewAILogRepo()
	seedAILogs(t, r)

	// 不加过滤：4 条全看，按新 → 旧
	list, total, err := r.List(model.AILogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("列 AI 记录失败: %v", err)
	}
	if total != 4 || len(list) != 4 {
		t.Fatalf("AI 记录 = %d 条 / total=%d，期望 4/4", len(list), total)
	}
	if list[0].Kind != model.MetricsAIKindPromQL {
		t.Errorf("应按 id 倒序，首条 kind = %q", list[0].Kind)
	}
	if list[1].Error == "" || list[1].Status != model.LogStatusError {
		t.Errorf("失败记录应带原因：%+v", list[1])
	}

	sum, err := r.Summarize(model.AILogFilter{})
	if err != nil {
		t.Fatalf("汇总 AI 记录失败: %v", err)
	}
	if sum.Calls != 4 || sum.OK != 3 || sum.Failed != 1 {
		t.Errorf("汇总计数不对：calls=%d ok=%d failed=%d", sum.Calls, sum.OK, sum.Failed)
	}
	if sum.TotalTokens != 410 {
		t.Errorf("汇总 token 不对：%d，期望 410", sum.TotalTokens)
	}
	if sum.EstimatedCalls != 1 {
		t.Errorf("估算条数 = %d，期望 1", sum.EstimatedCalls)
	}
	if len(sum.ByKind) != 3 { // sql / catalog / promql
		t.Errorf("按类型分组应有 3 组，实际 %+v", sum.ByKind)
	}
	if len(sum.ByMenu) != 2 { // mysql / metrics
		t.Errorf("按菜单分组应有 2 组，实际 %+v", sum.ByMenu)
	}
	if sum.FirstAt == "" || sum.LastAt == "" {
		t.Errorf("首末时间应非空：%+v", sum)
	}

	// 按菜单过滤：工具视图的核心口径
	if s, _ := r.Summarize(model.AILogFilter{Menu: model.AIMenuMySQL}); s.Calls != 3 || s.TotalTokens != 180 {
		t.Errorf("menu=mysql 过滤后 calls=%d tokens=%d，期望 3/180", s.Calls, s.TotalTokens)
	}
	if s, _ := r.Summarize(model.AILogFilter{Menu: model.AIMenuMetrics}); s.Calls != 1 || s.TotalTokens != 230 {
		t.Errorf("menu=metrics 过滤后 calls=%d tokens=%d，期望 1/230", s.Calls, s.TotalTokens)
	}
	// 菜单 + 类型
	if _, n, _ := r.List(model.AILogFilter{Menu: model.AIMenuMySQL, Kind: model.AILogKindCatalog, Page: 1, PageSize: 10}); n != 1 {
		t.Errorf("目录记录应有 1 条，实际 %d", n)
	}
	// 菜单 + 连接（工具里选中连接时的口径）
	if s, _ := r.Summarize(model.AILogFilter{Menu: model.AIMenuMySQL, ConnID: 1}); s.Calls != 2 || s.TotalTokens != 180 {
		t.Errorf("menu+conn 过滤后 calls=%d tokens=%d，期望 2/180", s.Calls, s.TotalTokens)
	}
	if _, n, _ := r.List(model.AILogFilter{Menu: model.AIMenuMySQL, ConnID: 2, Page: 1, PageSize: 10}); n != 1 {
		t.Errorf("另一连接的记录应有 1 条，实际 %d", n)
	}
	// 状态过滤
	if s, _ := r.Summarize(model.AILogFilter{Status: model.LogStatusError}); s.Calls != 1 {
		t.Errorf("按状态过滤后 calls=%d，期望 1", s.Calls)
	}

	// 分页：4 条记录、每页 3 条，第 2 页剩 1 条且不重复
	page2, _, err := r.List(model.AILogFilter{Page: 2, PageSize: 3})
	if err != nil {
		t.Fatalf("分页查询失败: %v", err)
	}
	if len(page2) != 1 || page2[0].ID == list[0].ID {
		t.Errorf("第 2 页结果不符：%+v", page2)
	}
}

func TestAILogClearAndPurge(t *testing.T) {
	setupAILogDB(t)
	r := NewAILogRepo()
	seedAILogs(t, r)

	// 造一条 40 天前的记录，保留期 30 天时它应被清掉、其余不动
	old, err := r.Add(&model.AILog{Menu: model.AIMenuMySQL, ConnID: 9, ConnName: "old",
		Kind: model.AILogKindSQL, Status: model.LogStatusOK, TotalTokens: 5})
	if err != nil {
		t.Fatalf("写旧记录失败: %v", err)
	}
	if _, err := store.DB.Exec(
		`UPDATE ai_logs SET created_at = datetime('now','localtime','-40 days') WHERE id = ?`, old); err != nil {
		t.Fatalf("改记录时间失败: %v", err)
	}
	n, err := r.PurgeBefore(30)
	if err != nil {
		t.Fatalf("按保留期清理失败: %v", err)
	}
	if n != 1 {
		t.Errorf("应清理 1 条记录，实际 %d", n)
	}
	// 保留期非正数时不清理任何数据
	if m, err := r.PurgeBefore(0); err != nil || m != 0 {
		t.Errorf("保留期为 0 时不应清理：%d err=%v", m, err)
	}

	// 按菜单清空只影响该菜单：清 mysql 时 metrics 必须留着
	deleted, err := r.Clear(model.AIMenuMySQL, 0)
	if err != nil {
		t.Fatalf("按菜单清空失败: %v", err)
	}
	if deleted != 3 {
		t.Errorf("应删 3 条 mysql 记录，实际 %d", deleted)
	}
	if _, total, _ := r.List(model.AILogFilter{Menu: model.AIMenuMetrics, Page: 1, PageSize: 10}); total != 1 {
		t.Errorf("metrics 记录不应被清，剩余 %d（期望 1）", total)
	}

	// 按菜单 + 连接清空：连接不符时不删
	if n, err := r.Clear(model.AIMenuMetrics, 99); err != nil || n != 0 {
		t.Errorf("连接不符时不应删：%d err=%v", n, err)
	}
	if n, err := r.Clear(model.AIMenuMetrics, 5); err != nil || n != 1 {
		t.Errorf("按菜单+连接应删 1 条：%d err=%v", n, err)
	}
	if _, total, _ := r.List(model.AILogFilter{Page: 1, PageSize: 10}); total != 0 {
		t.Errorf("记录应已清空，剩余 %d", total)
	}
}
