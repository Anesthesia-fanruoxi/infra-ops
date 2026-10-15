package aiopenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer 起一个按帧序列应答的流式端点；handler 收到请求后可读 body 做断言。
func sseServer(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func deltaChunk(t *testing.T, content string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"model":   "stream-model",
		"choices": []any{map[string]any{"delta": map[string]any{"content": content}}},
	})
	return "data: " + string(b) + "\n\n"
}

func TestChatStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, f := range []string{
			deltaChunk(t, "SELECT "),
			deltaChunk(t, "1"),
			": ping\n\n", // 心跳注释：必须被忽略
			`data: {"model":"stream-model","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}` + "\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = io.WriteString(w, f)
			fl.Flush()
		}
	}))
	defer srv.Close()

	var got []string
	c := New(Config{BaseURL: srv.URL, APIKey: "sk-s", Model: "m", Timeout: 5 * time.Second})
	res, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(text string) error {
		got = append(got, text)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	// 请求体：stream=true 且带 stream_options.include_usage
	if gotBody["stream"] != true {
		t.Errorf("请求体应带 stream:true：%v", gotBody)
	}
	so, _ := gotBody["stream_options"].(map[string]any)
	if so == nil || so["include_usage"] != true {
		t.Errorf("请求体应带 stream_options.include_usage：%v", gotBody)
	}
	// 增量按到达顺序回调，聚合内容与回调拼接一致
	if strings.Join(got, "") != "SELECT 1" {
		t.Errorf("增量回调 = %q", got)
	}
	if res.Content != "SELECT 1" {
		t.Errorf("聚合内容 = %q", res.Content)
	}
	if res.Model != "stream-model" {
		t.Errorf("模型名 = %q", res.Model)
	}
	if !res.Usage.FromAPI || res.Usage.TotalTokens != 11 {
		t.Errorf("流末帧 usage 应透出：%+v", res.Usage)
	}
}

func TestChatStreamNoDone(t *testing.T) {
	// 端点没发 [DONE] 直接断流：以收到的内容为准正常结束
	srv := sseServer(t, []string{deltaChunk(t, "abc"), deltaChunk(t, "def")})
	res, err := New(Config{BaseURL: srv.URL, Model: "m"}).ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream 失败: %v", err)
	}
	if res.Content != "abcdef" {
		t.Errorf("内容 = %q", res.Content)
	}
	if res.Usage.FromAPI {
		t.Error("没有 usage 帧时应退回估算")
	}
}

func TestChatStreamOptionsRetry(t *testing.T) {
	// 网关不认 stream_options：整单 400，去掉后重发一次
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		bodies = append(bodies, m)
		if _, has := m["stream_options"]; has {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, deltaChunk(t, "ok")+"data: [DONE]\n\n")
	}))
	defer srv.Close()

	res, err := New(Config{BaseURL: srv.URL, Model: "m"}).ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ChatStream 应重试成功: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("应恰好两次请求，实际 %d", len(bodies))
	}
	if _, has := bodies[1]["stream_options"]; has {
		t.Error("重试请求不应再带 stream_options")
	}
	if res.Content != "ok" {
		t.Errorf("内容 = %q", res.Content)
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	// 与 stream_options 无关的错误：不重试，直接带出错误信息
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	_, err := New(Config{BaseURL: srv.URL, Model: "m"}).ChatStream(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("应带出错误信息，实际: %v", err)
	}
}

func TestChatStreamErrorChunk(t *testing.T) {
	srv := sseServer(t, []string{`data: {"error":{"message":"rate limited"}}` + "\n\n"})
	_, err := New(Config{BaseURL: srv.URL, Model: "m"}).ChatStream(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("流中错误帧应终止并带出原因，实际: %v", err)
	}
}

func TestChatStreamEmpty(t *testing.T) {
	srv := sseServer(t, []string{"data: [DONE]\n\n"})
	if _, err := New(Config{BaseURL: srv.URL, Model: "m"}).ChatStream(context.Background(), nil, nil); err == nil {
		t.Error("空内容应报错")
	}
}

func TestChatStreamDeltaAbort(t *testing.T) {
	// 回调返回错误（客户端断开等）：立即中止并透出该错误
	srv := sseServer(t, []string{deltaChunk(t, "a"), deltaChunk(t, "b")})
	abort := context.Canceled
	_, err := New(Config{BaseURL: srv.URL, Model: "m"}).ChatStream(context.Background(), nil, func(string) error {
		return abort
	})
	if err == nil || err.Error() != abort.Error() {
		t.Fatalf("应透出回调错误，实际: %v", err)
	}
}

// TestChatStreamLongActiveNotKilled 持续有数据的长生成不受「整体超时」限制：
// 总时长超过 Timeout，但每次数据间隔都小于它 —— 修复前（http.Client.Timeout
// 是整体计时，连读 body 都算）这种正常生成会在读流阶段被掐死。
func TestChatStreamLongActiveNotKilled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			time.Sleep(50 * time.Millisecond)
			_, _ = io.WriteString(w, deltaChunk(t, "x"))
			fl.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "m", Timeout: 250 * time.Millisecond})
	res, err := c.ChatStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("持续有数据的长生成不该被判超时: %v", err)
	}
	if res.Content != "xxxxxxxx" {
		t.Fatalf("内容 = %q", res.Content)
	}
}

// TestChatStreamIdleTimeout 数据停在半途：空闲超过 Timeout 要报「未收到新数据」，
// 而不是把内部的 context canceled 原样透出。
func TestChatStreamIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, deltaChunk(t, "partial"))
		fl.Flush()
		time.Sleep(2 * time.Second) // 发一帧后卡住，超过 Timeout
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Model: "m", Timeout: 200 * time.Millisecond})
	_, err := c.ChatStream(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "未收到新数据") {
		t.Fatalf("空闲超时应报明确原因，实际 %v", err)
	}
}
