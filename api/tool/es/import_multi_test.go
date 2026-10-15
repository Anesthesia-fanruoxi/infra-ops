package es

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiSourceChainsFiles 多文件来源：按选择顺序串联，普通文件记录补上文件名 Origin
// （失败行要能指回哪个文件），单文件则保持原语义（不带 Origin）。
func TestMultiSourceChainsFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	if err := os.WriteFile(a, []byte("a1\na2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src, err := openSources([]string{a, b}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	recs, skips := drainSource(t, src)
	if len(skips) != 0 {
		t.Fatalf("不该有跳过: %v", skips)
	}
	if len(recs) != 3 {
		t.Fatalf("应合并出 3 条记录，实际 %d", len(recs))
	}
	if recs[0].Text != "a1" || recs[2].Text != "b1" {
		t.Fatalf("记录顺序不对: %+v", recs)
	}
	if recs[0].Origin != "a.log" || recs[2].Origin != "b.log" {
		t.Fatalf("多文件记录要带来源文件名: %q / %q", recs[0].Origin, recs[2].Origin)
	}
}

// TestOpenSourcesSinglePassthrough 单个文件不套 multiSource（与原单文件路径完全一致）。
func TestOpenSourcesSinglePassthrough(t *testing.T) {
	p := writeTemp(t, "one.log", "x\n")
	src, err := openSources([]string{p}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, ok := src.(*multiSource); ok {
		t.Fatal("单文件不该走 multiSource")
	}
	recs, _ := drainSource(t, src)
	if len(recs) != 1 || recs[0].Origin != "one.log" || recs[0].Text != "x" {
		t.Fatalf("单文件来源应保持原语义（Origin=文件名）: %+v", recs[0])
	}
}

func TestOpenSourcesEmpty(t *testing.T) {
	if _, err := openSources(nil, ""); err == nil {
		t.Fatal("空列表应报错")
	}
}

// TestSampleFromSourcesMerges 多文件抽样从合并流里等距抽，要能覆盖到靠后的文件。
func TestSampleFromSourcesMerges(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	if err := os.WriteFile(a, []byte("a1\na2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("b1\nb2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := sampleFromSources(context.Background(), []string{a, b}, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("want 3 条抽样，实际 %v", lines)
	}
	if !strings.Contains(strings.Join(lines, ","), "b") {
		t.Fatalf("抽样要覆盖到第二个文件: %v", lines)
	}
}

// TestValidateSources 校验要点名问题文件；空选择报错；合法单文件通过并回总大小。
func TestValidateSources(t *testing.T) {
	ok := writeTemp(t, "ok.log", "x")
	missing := filepath.Join(t.TempDir(), "nope.log")
	if _, _, err := validateSources([]string{ok, missing}); err == nil {
		t.Fatal("含不存在的文件应报错")
	} else if !strings.Contains(err.Error(), "nope.log") {
		t.Fatalf("错误要点名文件: %v", err)
	}
	if _, _, err := validateSources(nil); err == nil {
		t.Fatal("空选择应报错")
	}
	paths, total, err := validateSources([]string{"  " + ok + "  "})
	if err != nil || len(paths) != 1 || total <= 0 {
		t.Fatalf("单个有效文件应通过并回总大小: paths=%v total=%d err=%v", paths, total, err)
	}
}
