package aiopenai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.deepseek.com/", "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.deepseek.com/v1/", "https://api.deepseek.com/v1/chat/completions"},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1/chat/completions"},
		{"https://x.com/v1/chat/completions", "https://x.com/v1/chat/completions"},
		{"  https://x.com/v1  ", "https://x.com/v1/chat/completions"},
		// 非 /v1 的根路径必须原样保留，不能再拼一个 /v1（否则地址不存在）
		{"https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/openai", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
		{"https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{"https://qianfan.baidubce.com/v2", "https://qianfan.baidubce.com/v2/chat/completions"},
		{"", ""},
	}
	for _, c := range cases {
		got := New(Config{BaseURL: c.in}).Endpoint()
		if got != c.want {
			t.Errorf("Endpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModelsEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://api.deepseek.com", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/v1/chat/completions", "https://api.deepseek.com/v1/models"},
		{"https://api.xiaomimimo.com/v1/models", "https://api.xiaomimimo.com/v1/models"},
		{"  https://x.com/v1  ", "https://x.com/v1/models"},
		{"https://qianfan.baidubce.com/v2", "https://qianfan.baidubce.com/v2/models"},
		{"", ""},
	}
	for _, c := range cases {
		got := New(Config{BaseURL: c.in}).ModelsEndpoint()
		if got != c.want {
			t.Errorf("ModelsEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListModels(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mimo-v2.6-pro","object":"model"},{"id":"mimo-v2-flash","object":"model"},{"id":"  "}]}`))
	}))
	defer srv.Close()

	models, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "sk-test"}).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels 失败: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Errorf("请求路径 = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	// 空白 ID 应被剔除，其余按接口顺序保留
	if len(models) != 2 || models[0] != "mimo-v2.6-pro" || models[1] != "mimo-v2-flash" {
		t.Errorf("模型清单 = %+v", models)
	}
}

func TestListModelsErrors(t *testing.T) {
	// 401 + error 体
	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv401.Close()
	if _, err := New(Config{BaseURL: srv401.URL + "/v1", APIKey: "sk"}).ListModels(context.Background()); err == nil {
		t.Error("401 应报错")
	}

	// 200 但清单为空
	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srvEmpty.Close()
	if _, err := New(Config{BaseURL: srvEmpty.URL + "/v1"}).ListModels(context.Background()); err == nil {
		t.Error("空清单应报错")
	}

	// 缺地址时不应发出请求
	if _, err := New(Config{}).ListModels(context.Background()); err == nil {
		t.Error("缺地址应报错")
	}
}

func TestChat(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	var gotMessages []Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		gotModel, gotMessages = req.Model, req.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"deepseek-chat-0715","choices":[{"message":{"role":"assistant","content":"  SELECT 1  "}}],` +
			`"usage":{"prompt_tokens":31,"completion_tokens":7,"total_tokens":38}}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "deepseek-chat", Timeout: 5 * time.Second})
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if res.Content != "SELECT 1" {
		t.Errorf("内容未 trim：%q", res.Content)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("请求路径 = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotModel != "deepseek-chat" || len(gotMessages) != 1 || gotMessages[0].Content != "hi" {
		t.Errorf("请求体不符：model=%q messages=%+v", gotModel, gotMessages)
	}
	// 接口返回了 usage 时必须原样透出，且标记来源为接口
	if res.Usage != (Usage{PromptTokens: 31, CompletionTokens: 7, TotalTokens: 38, FromAPI: true}) {
		t.Errorf("usage = %+v", res.Usage)
	}
	if res.Model != "deepseek-chat-0715" {
		t.Errorf("服务端回显模型名 = %q", res.Model)
	}
}

// TestChatUsageFallback 覆盖「接口不回 usage」的网关：必须退回本地估算并标记来源。
func TestChatUsageFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"SELECT id FROM t_user WHERE name='张三'"}}]}`))
	}))
	defer srv.Close()

	res, err := New(Config{BaseURL: srv.URL, Model: "m"}).Chat(context.Background(), []Message{{Role: "user", Content: "查用户"}})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if res.Usage.FromAPI {
		t.Error("接口没回 usage 时不应标成 FromAPI")
	}
	// 中文按每字约 1 token，SQL 里的 ASCII 按每 4 字符约 1 token，量级必须在合理区间
	if res.Usage.PromptTokens <= 0 || res.Usage.CompletionTokens <= 0 {
		t.Errorf("估算值必须为正：%+v", res.Usage)
	}
	if got := res.Usage.PromptTokens + res.Usage.CompletionTokens; got != res.Usage.TotalTokens {
		t.Errorf("合计应等于输入+输出：%d != %d", got, res.Usage.TotalTokens)
	}
	t.Logf("估算输入=%d 输出=%d", res.Usage.PromptTokens, res.Usage.CompletionTokens)
}

// TestChatUsageOnlyTotal 少数网关只给 total_tokens：也应按接口值处理。
func TestChatUsageOnlyTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":120}}`))
	}))
	defer srv.Close()

	res, err := New(Config{BaseURL: srv.URL, Model: "m"}).Chat(context.Background(), nil)
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if !res.Usage.FromAPI || res.Usage.TotalTokens != 120 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},        // 不足 4 个 ASCII 也至少算 1
		{"abcd", 1},     // 4 个 ASCII 约 1 token
		{"abcdefgh", 2}, // 8 个 ASCII 约 2 token
		{"中文", 2},       // 汉字按每字 1 token
		{"中文测试", 4},     // 同上
		{"SELECT 1", 2}, // 8 个 ASCII（含空格）→ 2
		{"；", 1},        // 全角标点按宽字符
	}
	for _, c := range cases {
		if got := EstimateTokens(c.in); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	// 中文文本的估算量级必须接近字符数，而不是 1/4
	if got := EstimateTokens("这是一段用来验证估算口径的中文文本"); got < 16 {
		t.Errorf("中文估算偏小：%d", got)
	}
}

func TestChatErrors(t *testing.T) {
	// 服务端以 200 返回 error 字段（部分网关如此）
	srvErrBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"auth"}}`))
	}))
	defer srvErrBody.Close()
	if _, err := New(Config{BaseURL: srvErrBody.URL, Model: "m"}).Chat(context.Background(), nil); err == nil {
		t.Error("服务端返回 error 字段时应报错")
	}

	// 401 + error 体
	srv401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Unauthorized"}}`))
	}))
	defer srv401.Close()
	if _, err := New(Config{BaseURL: srv401.URL, Model: "m"}).Chat(context.Background(), nil); err == nil {
		t.Error("401 应报错")
	}

	// 200 但候选为空
	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srvEmpty.Close()
	if _, err := New(Config{BaseURL: srvEmpty.URL, Model: "m"}).Chat(context.Background(), nil); err == nil {
		t.Error("无候选应报错")
	}

	// 缺少模型名 / 地址时不应发出请求
	if _, err := New(Config{BaseURL: "http://127.0.0.1:1"}).Chat(context.Background(), nil); err == nil {
		t.Error("缺模型名应报错")
	}
	if _, err := New(Config{Model: "m"}).Chat(context.Background(), nil); err == nil {
		t.Error("缺地址应报错")
	}
}
