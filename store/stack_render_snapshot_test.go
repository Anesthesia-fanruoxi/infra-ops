package store

// 任务 09（套件目录化拆分）B1 渲染与蓝图基线。
//
// 目的：冻结「蓝图定义 / 流水线定义 / 各阶段渲染产物」三类后端行为，使拆分期可以做到
// 「渲染产物与基线逐字节一致」（设计文档 §8 P4 全局最硬验收点）。
//
// 用法：
//
//	go test ./store/ -run TestSnapshot                  # 与基线比对（默认）
//	SNAPSHOT_WRITE=1 go test ./store/ -run TestSnapshot # 生成 / 刷新基线
//
// 基线目录：workflow/tasks/09-stack-dir-split/baseline/{blueprint,pipeline,render}/

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stackSnapshotRoot = "../workflow/tasks/09-stack-dir-split/baseline"

// stackRenderPhases 需要冻结渲染产物的阶段（未知阶段同样冻结其错误文案）。
var stackRenderPhases = []string{"node", "bootstrap", "scale_out", "scale_in"}

// TestSnapshotBlueprints 冻结全部内置套件的蓝图（模式/变量/标签/最小主机数）。
func TestSnapshotBlueprints(t *testing.T) {
	dir := filepath.Join(stackSnapshotRoot, "blueprint")
	write := snapshotWriteMode(t, dir)

	for _, bp := range ListStackBlueprints() {
		b, err := json.MarshalIndent(bp, "", "  ")
		if err != nil {
			t.Fatalf("序列化蓝图失败(%s): %v", bp.Key, err)
		}
		assertSnapshot(t, filepath.Join(dir, bp.Key+".json"), append(b, '\n'), write)
	}
	// 列表顺序也是对外行为（前端卡片顺序）
	ord, _ := json.MarshalIndent(ListStackKeys(), "", "  ")
	assertSnapshot(t, filepath.Join(dir, "_order.json"), append(ord, '\n'), write)
}

// TestSnapshotPipelines 冻结套件 × 模式的流水线阶段定义（顺序 / Target / Component / FullOnly / Always）。
func TestSnapshotPipelines(t *testing.T) {
	dir := filepath.Join(stackSnapshotRoot, "pipeline")
	write := snapshotWriteMode(t, dir)

	for _, d := range ListStackDrivers() {
		bp := d.Blueprint()
		for _, m := range bp.Modes {
			phases := d.Pipeline(m.Key) // nil（单步流程）序列化为 null，同样冻结
			b, err := json.MarshalIndent(phases, "", "  ")
			if err != nil {
				t.Fatalf("序列化流水线失败(%s/%s): %v", d.Key(), m.Key, err)
			}
			assertSnapshot(t, filepath.Join(dir, d.Key()+"__"+m.Key+".json"), append(b, '\n'), write)
		}
	}
}

// TestSnapshotRenderScripts 冻结每套件每模式各阶段的渲染产物（含 @@ASSET@@ 注入结果）。
// 读不到脚本时冻结错误文案——错误行为同样是行为。
func TestSnapshotRenderScripts(t *testing.T) {
	dir := filepath.Join(stackSnapshotRoot, "render")
	write := snapshotWriteMode(t, dir)

	for _, d := range ListStackDrivers() {
		bp := d.Blueprint()
		for _, m := range bp.Modes {
			for _, ph := range stackRenderPhases {
				content, err := LoadStackPhase(d, m.Key, ph)
				if err != nil {
					content = "NO_SCRIPT: " + err.Error() + "\n"
				}
				assertSnapshot(t, filepath.Join(dir, d.Key()+"__"+m.Key+"__"+ph+".txt"), []byte(content), write)
			}
		}
	}
}

// snapshotWriteMode 返回是否处于写模式；写模式自动建目录。
func snapshotWriteMode(t *testing.T, dir string) bool {
	t.Helper()
	write := os.Getenv("SNAPSHOT_WRITE") == "1"
	if write {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("创建基线目录失败: %v", err)
		}
		return true
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("基线目录不存在（先跑 SNAPSHOT_WRITE=1 生成）: %v", err)
	}
	return false
}

// assertSnapshot 写模式写盘；比对模式逐字节比较并给出首个差异。
func assertSnapshot(t *testing.T, path string, got []byte, write bool) {
	t.Helper()
	if write {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("写入基线失败(%s): %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取基线失败(%s)（补生成请跑 SNAPSHOT_WRITE=1）: %v", filepath.Base(path), err)
	}
	if string(want) == string(got) {
		return
	}
	t.Fatalf("与基线不一致: %s\n%s", filepath.Base(path), firstLineDiff(string(want), string(got)))
}

// firstLineDiff 首个差异行及其上下文。
func firstLineDiff(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w == g {
			continue
		}
		lo := i - 3
		if lo < 0 {
			lo = 0
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "第 %d 行起差异（基线 %d 行 / 当前 %d 行）：\n", i+1, len(wl), len(gl))
		for j := lo; j <= i && j < len(wl); j++ {
			fmt.Fprintf(&sb, "  基线 %d | %s\n", j+1, wl[j])
		}
		for j := lo; j <= i && j < len(gl); j++ {
			fmt.Fprintf(&sb, "  当前 %d | %s\n", j+1, gl[j])
		}
		return sb.String()
	}
	return "(内容相同)"
}
