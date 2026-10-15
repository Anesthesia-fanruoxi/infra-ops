package es

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, url string) *escClient {
	t.Helper()
	c, err := newESCClient(url, false, "anonymous", "", "")
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	return c
}

// TestBulkWriterPerItemStatus 这是写入层最要紧的一条：HTTP 200 但部分失败。
// 只看状态码会把「一批全被 mapping 拒绝」当成导入成功，用户以为数据进去了。
func TestBulkWriterPerItemStatus(t *testing.T) {
	var gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"took":5,"errors":true,"items":[
			{"index":{"status":201}},
			{"index":{"status":400,"error":{"type":"mapper_parsing_exception","reason":"failed to parse field [status]"}}},
			{"index":{"status":201}}
		]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	w := newBulkWriter(newTestClient(t, srv.URL), "logs", "", 100)
	for i := 0; i < 3; i++ {
		if err := w.Add(ctx, map[string]any{"a": i}, "x.jsonl:"+string(rune('1'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatalf("批量提交不该报错: %v", err)
	}

	if gotCT != "application/x-ndjson" {
		t.Fatalf("_bulk 必须用 application/x-ndjson，实际 %q", gotCT)
	}
	lines := strings.Split(strings.TrimRight(gotBody, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("3 条文档应产生 6 行 NDJSON（动作行 + 文档行），实际 %d 行:\n%s", len(lines), gotBody)
	}
	if lines[0] != `{"index":{"_index":"logs"}}` || lines[1] != `{"a":0}` {
		t.Fatalf("NDJSON 行格式不对: %q / %q", lines[0], lines[1])
	}

	if w.Succeeded != 2 || w.Failed != 1 {
		t.Fatalf("应按 items[].status 计数：成功 2 失败 1，实际 %d / %d", w.Succeeded, w.Failed)
	}
	fails := w.Failures()
	if len(fails) != 1 || !strings.Contains(fails[0], "x.jsonl:2") {
		t.Fatalf("失败明细要能指回原始行: %v", fails)
	}
	if !strings.Contains(fails[0], "failed to parse field [status]") {
		t.Fatalf("失败明细要带 ES 给的原因: %v", fails)
	}
}

func TestBulkWriterAllSucceed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":false,"items":[{"index":{"status":201}},{"index":{"status":201}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	w := newBulkWriter(newTestClient(t, srv.URL), "logs", "", 100)
	w.Add(ctx, map[string]any{"a": 1}, "l:1")
	w.Add(ctx, map[string]any{"a": 2}, "l:2")
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Succeeded != 2 || w.Failed != 0 || len(w.Failures()) != 0 {
		t.Fatalf("全部成功时不该有失败记录: %d / %d / %v", w.Succeeded, w.Failed, w.Failures())
	}
}

// TestBulkWriterIdField 指定 _id 字段：幂等覆盖靠它，写错位置就等于每次导入都在堆重复数据。
func TestBulkWriterIdField(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"errors":false,"items":[{"index":{"status":201}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	w := newBulkWriter(newTestClient(t, srv.URL), "logs", "trace_id", 100)
	w.Add(ctx, map[string]any{"trace_id": "abc-1", "msg": "x"}, "l:1")
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `"_id":"abc-1"`) {
		t.Fatalf("指定的字段值要作为 _id 下发，实际:\n%s", gotBody)
	}
	if !strings.Contains(gotBody, `"trace_id":"abc-1"`) {
		t.Fatalf("作为 _id 的字段本身仍应保留在文档里:\n%s", gotBody)
	}
}

// TestBulkWriterNoIdWhenFieldMissing 文档里没有该字段时不硬造 _id，交给 ES 自动生成。
func TestBulkWriterNoIdWhenFieldMissing(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		io.WriteString(w, `{"errors":false,"items":[{"index":{"status":201}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	w := newBulkWriter(newTestClient(t, srv.URL), "logs", "trace_id", 100)
	w.Add(ctx, map[string]any{"msg": "x"}, "l:1")
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotBody, `"_id"`) {
		t.Fatalf("字段缺失时不该下发 _id（否则会有空 _id 冲突）:\n%s", gotBody)
	}
}

func TestBulkWriterHTTPErrorIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"type":"cluster_block_exception","reason":"index read-only"}}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	w := newBulkWriter(newTestClient(t, srv.URL), "logs", "", 100)
	w.Add(ctx, map[string]any{"a": 1}, "l:1")
	err := w.Flush(ctx)
	if err == nil || !strings.Contains(err.Error(), "index read-only") {
		t.Fatalf("整批请求失败要报错并带原因，实际 %v", err)
	}
}

// TestBulkWriterAutoFlush 到批量上限自动提交一次，不必等调用方显式 Flush。
func TestBulkWriterAutoFlush(t *testing.T) {
	batches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batches++
		io.WriteString(w, `{"errors":false,"items":[{"index":{"status":201}},{"index":{"status":201}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	w := newBulkWriter(newTestClient(t, srv.URL), "logs", "", 2)
	w.Add(ctx, map[string]any{"a": 1}, "l:1")
	w.Add(ctx, map[string]any{"a": 2}, "l:2") // 达到 2 条 → 自动提交
	if batches != 1 {
		t.Fatalf("达到批量上限应自动提交一次，实际 %d 次", batches)
	}
	if err := w.Flush(ctx); err != nil { // 缓冲已空，不该再发请求
		t.Fatal(err)
	}
	if batches != 1 {
		t.Fatalf("缓冲为空时 Flush 不该发请求，实际共 %d 次", batches)
	}
}
