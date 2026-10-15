package es

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/repo"
	"infra-ops/store/setting"
)

// grokSetup 搭最小环境：临时库 + 一条 ES 连接 +（可选）指向 mock 的 AI 配置。
// 与包内既有测试助手不重名，避免并行会话改动互相踩。
func grokSetup(t *testing.T, aiBaseURL string) (*gin.Engine, int64) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := store.Open(filepath.Join("data", "infra-ops.db")); err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(store.Close)

	esRepo := repo.NewESRepo()
	connID, err := esRepo.Create(&model.ESConn{Name: "grok-test", URL: "http://127.0.0.1:9200", AuthType: "anonymous"})
	if err != nil {
		t.Fatalf("建连接失败: %v", err)
	}
	sr := setting.NewSettingsRepo()
	if aiBaseURL != "" {
		for k, v := range map[string]string{
			setting.SettingAIEnabled: "1",
			setting.SettingAIBaseURL: aiBaseURL,
			setting.SettingAIModel:   "grok-mock",
		} {
			if err := sr.Set(k, v); err != nil {
				t.Fatalf("写 AI 配置失败: %v", err)
			}
		}
	}
	// 注入字段缓存仓储：生成 logstash 配置要按它决定 remove_field
	//（缓存为空时退回候选池，两种情况都还能测）
	h := NewHandler(esRepo, nil).WithAI(sr, repo.NewAILogRepo()).WithFieldCache(repo.NewESFieldRepo())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/es/:id/grok/parse", h.GrokParse)
	r.POST("/api/es/:id/grok/ai", h.GrokAI)
	r.POST("/api/es/:id/grok/logstash", h.GrokLogstash)
	return r, connID
}

// grokPost JSON POST 快捷方式。AI 端点已流式化：响应体是 SSE 帧时，
// 取 final / error 帧拼成与旧同步接口等价的 {code,message,data} 信封，断言写法保持不变。
func grokPost(t *testing.T, r *gin.Engine, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	var out map[string]any
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		out = sseEnvelope(t, w.Body.String())
	} else {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w, out
}

// sseEnvelope 把 SSE 响应解成 {"code":0,"data":<final 帧>}；error 帧转成业务错误码。
// delta / reset 帧是展示用的草稿通道，测试断言不依赖，直接忽略。
func sseEnvelope(t *testing.T, raw string) map[string]any {
	t.Helper()
	event := ""
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		switch event {
		case "error":
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(payload), &e)
			return map[string]any{"code": float64(500), "message": e.Message, "data": nil}
		case "final":
			var data map[string]any
			_ = json.Unmarshal([]byte(payload), &data)
			return map[string]any{"code": float64(0), "message": "ok", "data": data}
		}
	}
	t.Fatalf("SSE 响应里没有 final / error 帧: %s", raw)
	return nil
}

// aiCallRecorder 记录 mock 收到的每次请求的 messages 数组。
// mock handler 在服务端 goroutine 写、测试在主 goroutine 读，加锁防竞态。
type aiCallRecorder struct {
	mu   sync.Mutex
	msgs [][]testChatMsg
}

// testChatMsg 请求体 messages 数组里的一条（OpenAI 兼容）。
type testChatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (r *aiCallRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func (r *aiCallRecorder) at(i int) []testChatMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.msgs[i]
}

// fakeAIServer 按调用次序返回预设内容（用完后重复最后一条），记录每次请求的 messages。
// 按 SSE 流式协议应答（处理器走 ChatStream）：内容按 rune 切两半模拟增量到达，
// usage 随末帧回，最后 [DONE] 收尾——与真实 OpenAI 兼容端点同口径。
func fakeAIServer(t *testing.T, contents []string) (*httptest.Server, *aiCallRecorder) {
	t.Helper()
	rec := &aiCallRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []testChatMsg `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		rec.mu.Lock()
		rec.msgs = append(rec.msgs, req.Messages)
		i := len(rec.msgs) - 1
		rec.mu.Unlock()
		c := contents[len(contents)-1]
		if i < len(contents) {
			c = contents[i]
		}
		w.Header().Set("Content-Type", "text/event-stream")
		runes := []rune(c)
		mid := len(runes) / 2
		for _, part := range []string{string(runes[:mid]), string(runes[mid:])} {
			if part == "" {
				continue
			}
			fmt.Fprintf(w, "data: {\"model\":\"grok-mock\",\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", strconv.Quote(part))
		}
		fmt.Fprint(w, "data: {\"model\":\"grok-mock\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

const grokTestSample = "2026-10-10 14:20:31 ERROR [order] create failed: timeout\n" +
	"2026-10-10 14:20:32 INFO [order] ok\n" +
	"this line does not match at all"

const grokTestPattern = `%{TIMESTAMP_ISO8601:ts} %{LOGLEVEL:level} \[%{DATA:ctx}\] %{GREEDYDATA:message}`

// grokTestSampleHit 两行都能被 grokTestPattern 整行命中的样例：生成侧「一次通过」的场景。
const grokTestSampleHit = "2026-10-10 14:20:31 ERROR [order] create failed: timeout\n" +
	"2026-10-10 14:20:32 INFO [order] ok"

// grokAIBadPattern 样例里并不存在的竖线：正则编译得过，但整行必不命中
// —— 正是线上踩到的那类「按格式定义推断出的字面分隔符」。
const grokAIBadPattern = `%{TIMESTAMP_ISO8601:ts} \| %{LOGLEVEL:level} \[%{DATA:ctx}\] %{GREEDYDATA:message}`

// grokAIBadCompile 真实反馈里的那条：开头的 [ 没转义，被 RE2 当成字符类开头，
// 展开后 `)-(` 被当成范围 ⇒ invalid character class range（整份 pattern 根本加载不了）。
const grokAIBadCompile = `[%{TIMESTAMP_ISO8601:ts},%{BASE10NUM:nanosecond}]\s+\[%{IP:ip}\]\s+\[%{WORD:method}\s+%{DATA:status_or_dash}\s+%{URI:uri}]`

// grokAICompilesNoHit 语法合法但在这两条样例上一行都不命中 —— 用来验证
// 「首稿编译不过时，命中数不该成为采纳的前提」。
const grokAICompilesNoHit = `%{WORD:a} %{WORD:b}`

// replyJSON 把模型回复结构编成 JSON（省得手写转义）。
func replyJSON(r grokReply) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// TestGrokParseHTTP 走完整 HTTP 链路：命中 / 未命中 / 保序统计。
func TestGrokParseHTTP(t *testing.T) {
	r, id := grokSetup(t, "")
	path := fmt.Sprintf("/api/es/%d/grok/parse", id)

	_, out := grokPost(t, r, path, map[string]any{"patterns": []string{grokTestPattern}, "text": grokTestSample})
	if out["code"].(float64) != 0 {
		t.Fatalf("解析应成功: %v", out)
	}
	data, _ := out["data"].(map[string]any)
	if data["matched"].(float64) != 2 || data["missed"].(float64) != 1 || data["total"].(float64) != 3 {
		t.Fatalf("统计不符: %v", out)
	}
	lines := data["lines"].([]any)
	first := lines[0].(map[string]any)
	fields := map[string]string{}
	for _, f := range first["fields"].([]any) {
		item := f.(map[string]any)
		fields[item["name"].(string)] = item["value"].(string)
	}
	if fields["ts"] != "2026-10-10 14:20:31" || fields["level"] != "ERROR" || fields["message"] != "create failed: timeout" {
		t.Fatalf("首行字段不对: %v", fields)
	}
	last := lines[2].(map[string]any)
	if last["matched"].(bool) || last["pattern_index"].(float64) != -1 {
		t.Fatalf("未命中行应标 matched=false / pattern_index=-1: %v", last)
	}
}

// TestGrokParseErrors 编译错误必须带 pattern 名与原因（前端直接展示给用户改）。
func TestGrokParseErrors(t *testing.T) {
	r, id := grokSetup(t, "")
	path := fmt.Sprintf("/api/es/%d/grok/parse", id)

	_, out := grokPost(t, r, path, map[string]any{"patterns": []string{`%{NO_SUCH:x}`}, "text": "abc"})
	if out["code"].(float64) != esCodeGrokInvalid {
		t.Fatalf("未定义 pattern 应报 4016: %v", out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "未定义的 pattern") {
		t.Fatalf("错误信息应指明未定义: %v", out)
	}

	_, out2 := grokPost(t, r, path, map[string]any{"patterns": []string{`(?<=x)y`}, "text": "xy"})
	if !strings.Contains(out2["message"].(string), "RE2") {
		t.Fatalf("不可用语法应提示 RE2 方言: %v", out2)
	}

	_, out3 := grokPost(t, r, path, map[string]any{"patterns": []string{"  "}, "text": "abc"})
	if out3["code"].(float64) != esCodeGrokInvalid {
		t.Fatalf("空 pattern 应报 4016: %v", out3)
	}
}

// TestGrokParsePartial 整行不中但局部命中时要回 Partial —— 排错靠它。
func TestGrokParsePartial(t *testing.T) {
	r, id := grokSetup(t, "")
	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/parse", id),
		map[string]any{"patterns": []string{`%{IP:ip}`}, "text": "10.0.0.1 extra words"})
	data, _ := out["data"].(map[string]any)
	if data["partial"].(float64) != 1 || data["matched"].(float64) != 0 {
		t.Fatalf("应记为部分命中: %v", out)
	}
	line := data["lines"].([]any)[0].(map[string]any)
	if !line["partial"].(bool) {
		t.Fatalf("该行 partial 应为 true: %v", line)
	}
}

// TestGrokAI 首稿就能整行命中样例：一次模型调用就结束（本地校验通过时不重试），
// final 带 verify 结果与字段解释，并落一条调用记录。
func TestGrokAI(t *testing.T) {
	good := replyJSON(grokReply{
		Patterns: []string{grokTestPattern}, Explain: "标准应用日志",
		Fields: []grokAIField{{Name: "ts", Desc: "时间"}, {Name: "level", Desc: "级别"}},
	})
	srv, rec := fakeAIServer(t, []string{good})
	r, id := grokSetup(t, srv.URL)

	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id),
		map[string]any{"sample": grokTestSampleHit, "hint": "把上下文也拆出来"})
	if out["code"].(float64) != 0 {
		t.Fatalf("AI 生成应成功: %v", out)
	}
	if n := rec.count(); n != 1 {
		t.Fatalf("首稿已整行命中就不该再花一次模型调用，实际 %d 次", n)
	}
	data, _ := out["data"].(map[string]any)
	verify, _ := data["verify"].(map[string]any)
	if verify == nil || !verify["ok"].(bool) || verify["matched"].(float64) != 2 ||
		verify["total"].(float64) != 2 || verify["attempts"].(float64) != 1 || verify["adopted_retry"].(bool) {
		t.Fatalf("verify 结果不符（应 2/2 一次通过）: %v", verify)
	}
	if !strings.Contains(data["patterns"].([]any)[0].(string), "GREEDYDATA") {
		t.Fatalf("pattern 应原样返回: %v", data["patterns"])
	}
	if len(data["fields"].([]any)) != 2 {
		t.Fatalf("字段解释应随 final 返回: %v", data["fields"])
	}

	// 调用记录：menu=es / kind=grok，且带上题面与生成的 pattern
	var menu, kind, question, expr string
	if err := store.DB.QueryRow(
		`SELECT menu, kind, question, expr FROM ai_logs ORDER BY id DESC LIMIT 1`).
		Scan(&menu, &kind, &question, &expr); err != nil {
		t.Fatalf("读调用记录失败: %v", err)
	}
	if menu != model.AIMenuES || kind != model.AILogKindGrok || question != "把上下文也拆出来" || expr == "" {
		t.Fatalf("调用记录内容不符: menu=%s kind=%s q=%s expr=%s", menu, kind, question, expr)
	}
}

// TestGrokAIRetry 首稿整行不命中时：本地校验拦下来 → 带失败现场回喂一次 → 采纳修正稿。
// 这是「模型把格式定义里的变量顺序当成字面分隔符」这类错误的兜底（它自己判断不了，跑一遍就知道）。
func TestGrokAIRetry(t *testing.T) {
	srv, rec := fakeAIServer(t, []string{
		replyJSON(grokReply{Patterns: []string{grokAIBadPattern}, Explain: "第一版"}),
		replyJSON(grokReply{Patterns: []string{grokTestPattern}, Explain: "修好的"}),
	})
	r, id := grokSetup(t, srv.URL)

	w, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id),
		map[string]any{"sample": grokTestSampleHit, "hint": "拆出上下文"})
	if out["code"].(float64) != 0 {
		t.Fatalf("AI 生成应成功: %v", out)
	}
	if n := rec.count(); n != 2 {
		t.Fatalf("首稿不命中应回喂一次（共两次调用），实际 %d 次", n)
	}
	if !strings.Contains(w.Body.String(), "event:phase") {
		t.Fatalf("重试前应推一帧 phase（界面要能显示「正在修正」）: %s", w.Body.String())
	}
	data, _ := out["data"].(map[string]any)
	verify, _ := data["verify"].(map[string]any)
	if !verify["ok"].(bool) || verify["attempts"].(float64) != 2 || !verify["adopted_retry"].(bool) {
		t.Fatalf("应采纳修正稿（2/2 通过）: %v", verify)
	}
	if got := data["patterns"].([]any)[0].(string); got != grokTestPattern {
		t.Fatalf("应返回修正稿而非首稿: %q", got)
	}
	if data["explain"].(string) != "修好的" {
		t.Fatalf("说明应与采纳的那一版一致: %v", data["explain"])
	}

	// 第二次请求：assistant 是首稿 JSON，user 是失败现场（含未命中行原文）
	msgs := rec.at(1)
	if len(msgs) != 4 || msgs[2].Role != "assistant" || !strings.Contains(msgs[2].Content, "\\|") {
		t.Fatalf("第三条应是回灌的首稿 JSON（含那条错的正则）: %v", msgs)
	}
	if !strings.Contains(msgs[3].Content, "无法整行匹配") ||
		!strings.Contains(msgs[3].Content, "2026-10-10 14:20:31 ERROR") {
		t.Fatalf("第四条应带上失败现场（含未命中行原文）: %v", msgs[3])
	}
}

// TestGrokAIRetryNotAdopted 修正稿没有更优（命中数没涨）时必须保留首稿 —— 免得越修越差。
func TestGrokAIRetryNotAdopted(t *testing.T) {
	matchFirst := `%{TIMESTAMP_ISO8601:ts} %{LOGLEVEL:level} \[%{DATA:ctx}\] create failed: %{GREEDYDATA:message}`
	matchSecond := `%{TIMESTAMP_ISO8601:ts} %{LOGLEVEL:level} \[%{DATA:ctx}\] ok`
	srv, rec := fakeAIServer(t, []string{
		replyJSON(grokReply{Patterns: []string{matchFirst}, Explain: "只命中第一行"}),
		replyJSON(grokReply{Patterns: []string{matchSecond}, Explain: "只命中第二行"}),
	})
	r, id := grokSetup(t, srv.URL)

	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id),
		map[string]any{"sample": grokTestSampleHit, "hint": "拆出上下文"})
	if n := rec.count(); n != 2 {
		t.Fatalf("1/2 命中不算通过，仍会重试一次，实际 %d 次", n)
	}
	data, _ := out["data"].(map[string]any)
	verify, _ := data["verify"].(map[string]any)
	if verify["ok"].(bool) || verify["adopted_retry"].(bool) {
		t.Fatalf("修正稿没有更优就不该采纳: %v", verify)
	}
	if got := data["patterns"].([]any)[0].(string); got != matchFirst {
		t.Fatalf("应保留首稿: %q", got)
	}
	if verify["matched"].(float64) != 1 || verify["missed"].(float64) != 1 {
		t.Fatalf("verify 应如实回报 1 命中 1 未命中: %v", verify)
	}
}

// TestGrokAIRetryCompileFirst 首稿「根本编译不过」时，只要修正稿能编译就必须采纳它。
// 来自真实反馈：模型把样例里的字面方括号照抄进正则（[ 成了字符类开头），编译失败后命中数恒为 0，
// 于是被「命中数更大才采纳」的判据判成「没更优」—— 用户最终拿到的仍是那份 logstash 加载不了的 pattern。
func TestGrokAIRetryCompileFirst(t *testing.T) {
	srv, rec := fakeAIServer(t, []string{
		replyJSON(grokReply{Patterns: []string{grokAIBadCompile}, Explain: "首稿"}),
		replyJSON(grokReply{Patterns: []string{grokAICompilesNoHit}, Explain: "能编译了"}),
	})
	r, id := grokSetup(t, srv.URL)

	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id),
		map[string]any{"sample": grokTestSampleHit, "hint": "拆出上下文"})
	if n := rec.count(); n != 2 {
		t.Fatalf("首稿编译失败必须触发一次修正，实际 %d 次", n)
	}
	// 回喂内容要写明是编译失败并给出修法，否则模型只会在结构上瞎猜
	if fb := rec.at(1)[3].Content; !strings.Contains(fb, "编译失败") || !strings.Contains(fb, "字符类") {
		t.Fatalf("重试提示要带上编译失败现场与修法: %s", fb)
	}
	data, _ := out["data"].(map[string]any)
	verify, _ := data["verify"].(map[string]any)
	if got := data["patterns"].([]any)[0].(string); got != grokAICompilesNoHit {
		t.Fatalf("首稿编译不过时，修正稿只要能编译就必须采纳（哪怕一行未命中）: %q", got)
	}
	if !verify["adopted_retry"].(bool) {
		t.Fatalf("adopted_retry 应标 true: %v", verify)
	}
	if errText, _ := verify["error"].(string); errText != "" {
		t.Fatalf("采纳的那版不该带编译错误: %v", verify)
	}
}

// TestGrokRetryBetter 采纳判据的两级比较：编译通过优先于命中数。
func TestGrokRetryBetter(t *testing.T) {
	ok1 := grokVerifyResult{Matched: 1, Total: 2}
	ok2 := grokVerifyResult{Matched: 2, Total: 2}
	ok0 := grokVerifyResult{Total: 2}
	bad := grokVerifyResult{Compile: "invalid character class range", Total: 2}
	cases := []struct {
		name       string
		prev, next grokVerifyResult
		want       bool
	}{
		{"首稿编译不过 + 修正稿能编译 ⇒ 无条件采纳", bad, ok0, true},
		{"两边都编译不过 ⇒ 不采纳", bad, grokVerifyResult{Compile: "x", Total: 2}, false},
		{"两边都能编译 ⇒ 比命中数（更高才采纳）", ok1, ok2, true},
		{"两边都能编译 ⇒ 命中数不涨就不采纳", ok2, ok2, false},
		{"两边都能编译 ⇒ 命中数下降不采纳", ok2, ok1, false},
		{"首稿能编译 + 修正稿编译不过 ⇒ 不采纳（不能拿坏件换好件）", ok1, grokVerifyResult{Compile: "y", Total: 2}, false},
	}
	for _, c := range cases {
		if got := grokRetryBetter(c.prev, c.next); got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// TestGrokAIFollowup 追加生成：第二轮的 messages 里应有「上一版 JSON（assistant）+ 追加要求（user）」，
// 且要求里带上最初那轮的补充要求原文；调用记录题面加「追加：」前缀。
func TestGrokAIFollowup(t *testing.T) {
	first := replyJSON(grokReply{Patterns: []string{grokTestPattern}, Explain: "第一版说明"})
	second := replyJSON(grokReply{Patterns: []string{grokTestPattern + `%{DATA:extra}`}, Explain: "第二版说明"})
	srv, rec := fakeAIServer(t, []string{first, second})
	r, id := grokSetup(t, srv.URL)
	path := fmt.Sprintf("/api/es/%d/grok/ai", id)

	_, out1 := grokPost(t, r, path, map[string]any{"sample": grokTestSampleHit, "hint": "拆出上下文"})
	if out1["code"].(float64) != 0 {
		t.Fatalf("首轮生成应成功: %v", out1)
	}
	firstPatterns := out1["data"].(map[string]any)["patterns"].([]any)

	_, out2 := grokPost(t, r, path, map[string]any{
		"sample": grokTestSampleHit, "hint": "追加要求：extra 也拆出来",
		"prev": map[string]any{
			"hint": "拆出上下文", "patterns": firstPatterns,
			"explain": out1["data"].(map[string]any)["explain"],
		},
	})
	if out2["code"].(float64) != 0 {
		t.Fatalf("追加生成应成功: %v", out2)
	}
	if n := rec.count(); n != 2 {
		t.Fatalf("应恰好两次调用，实际 %d", n)
	}

	msgs := rec.at(1)
	if len(msgs) != 4 {
		t.Fatalf("追加轮的 messages 应为 system/user/assistant/user 四条，实际 %d: %v", len(msgs), msgs)
	}
	if msgs[2].Role != "assistant" || !strings.Contains(msgs[2].Content, "GREEDYDATA") {
		t.Fatalf("第三条应是回灌的上一版 JSON（含 pattern）: %v", msgs[2])
	}
	if msgs[3].Role != "user" ||
		!strings.Contains(msgs[3].Content, "拆出上下文") ||
		!strings.Contains(msgs[3].Content, "追加要求：extra 也拆出来") {
		t.Fatalf("第四条应是追加要求且带最初补充要求原文: %v", msgs[3])
	}

	// 调用记录：第二轮的题面加「追加：」前缀，与首轮区分
	var question string
	if err := store.DB.QueryRow(
		`SELECT question FROM ai_logs ORDER BY id DESC LIMIT 1`).Scan(&question); err != nil {
		t.Fatalf("读调用记录失败: %v", err)
	}
	if question != "追加：追加要求：extra 也拆出来" {
		t.Fatalf("追加轮题面应带前缀: %q", question)
	}
}

// TestGrokAIUnavailable 未启用 AI 时给出可操作的报错，而不是 500。
func TestGrokAIUnavailable(t *testing.T) {
	r, id := grokSetup(t, "")
	w, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/ai", id), map[string]any{"sample": "a b c"})
	if w.Code != http.StatusOK || out["code"].(float64) == 0 {
		t.Fatalf("未启用 AI 应返回业务错误: %v", out)
	}
	if !strings.Contains(out["message"].(string), "AI") {
		t.Fatalf("错误信息应提示 AI 未启用: %v", out)
	}
}
