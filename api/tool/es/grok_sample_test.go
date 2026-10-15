package es

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSampleFromSourceDiverse 等距抽样要能同时覆盖占位行与真实值行——
// 这正是「用真实数据驱动生成」的意义：样例里若只有带 IP 的行，模型就会生成只认 IP 的过严 pattern。
func TestSampleFromSourceDiverse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.log")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		if i%2 == 0 {
			b.WriteString("[ts] [-] [GET - /a - 1 200]\n") // 占位行
		} else {
			b.WriteString("[ts] [10.0.0.1] [GET - /a - 1 200]\n") // 带 IP 行
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := sampleFromSource(context.Background(), path, "", 20)
	if err != nil {
		t.Fatalf("sampleFromSource: %v", err)
	}
	if len(lines) != 20 {
		t.Fatalf("len = %d, want 20", len(lines))
	}
	var dash, ip int
	for _, l := range lines {
		if strings.Contains(l, "[-]") {
			dash++
		}
		if strings.Contains(l, "[10.0.0.1]") {
			ip++
		}
	}
	if dash == 0 || ip == 0 {
		t.Fatalf("等距抽样应同时覆盖占位行与 IP 行，实际 dash=%d ip=%d", dash, ip)
	}
}

// TestSampleFromSourceFewerThanWant 行数不足 want 时全返回，不做抽样。
func TestSampleFromSourceFewerThanWant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "small.log")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := sampleFromSource(context.Background(), path, "", 20)
	if err != nil {
		t.Fatalf("sampleFromSource: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("len = %d, want 3", len(lines))
	}
}

// TestSampleFromSourceSkipsJSON 结构化 JSON 记录（rec.Doc != nil）不参与 grok 取样，返回空。
func TestSampleFromSourceSkipsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(path, []byte("{\"a\":1}\n{\"b\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := sampleFromSource(context.Background(), path, "", 20)
	if err != nil {
		t.Fatalf("sampleFromSource: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("结构化 JSON 不应产出 grok 样例，got %v", lines)
	}
}

// TestGrokAIFromSource 只给 source、不给 sample 时，后端应从文件取样并送进模型。
// 断言落在「模型实际收到的 user 消息里出现文件中的行」，验证 source → sampleFromSource → prompt 的接线。
func TestGrokAIFromSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte(grokTestSampleHit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := replyJSON(grokReply{Patterns: []string{grokTestPattern}, Explain: "应用日志"})
	srv, rec := fakeAIServer(t, []string{good})
	r, id := grokSetup(t, srv.URL)

	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id), map[string]any{"source": path})
	if out["code"].(float64) != 0 {
		t.Fatalf("从文件取样生成应成功: %v", out)
	}
	var user string
	for _, m := range rec.at(0) {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if !strings.Contains(user, "create failed: timeout") {
		t.Fatalf("模型应收到从文件抽出的样例行，实际 user 消息: %q", user)
	}
}

// TestGrokAISampleOverridesSource 手贴 sample 与 source 同时给出时，以 sample 优先（用户显式给的）。
func TestGrokAISampleOverridesSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte("FILE_LINE_SHOULD_NOT_APPEAR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := replyJSON(grokReply{Patterns: []string{grokTestPattern}, Explain: "应用日志"})
	srv, rec := fakeAIServer(t, []string{good})
	r, id := grokSetup(t, srv.URL)

	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id),
		map[string]any{"sample": grokTestSampleHit, "source": path})
	if out["code"].(float64) != 0 {
		t.Fatalf("生成应成功: %v", out)
	}
	var user string
	for _, m := range rec.at(0) {
		if m.Role == "user" {
			user = m.Content
		}
	}
	if strings.Contains(user, "FILE_LINE_SHOULD_NOT_APPEAR") {
		t.Fatalf("手贴 sample 非空时不应再从文件取样: %q", user)
	}
	if !strings.Contains(user, "create failed: timeout") {
		t.Fatalf("应使用手贴样例: %q", user)
	}
}
