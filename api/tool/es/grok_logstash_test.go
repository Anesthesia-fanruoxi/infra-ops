// grok_logstash_test.go Logstash 配置生成：本地拼装是「必须逐字符正确」的部分，
// 所以这里既测纯函数（拼装/字段提取/校验），也测 HTTP 端点（AI 三条分支的降级行为）。
//
// 重点覆盖两类只在真机上才会暴露的缺陷：
//   - pattern 里的反斜杠被「顺手转义」成 \\d，logstash 拿到的正则直接失配；
//   - 模型幻觉出的字段名进了 remove_field，把刚解析出来的业务字段删掉。
package es

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"infra-ops/common/grok"
)

// ---------------- 纯函数：拼装 ----------------

const logstashSamplePattern = `%{TIMESTAMP_ISO8601:time} %{NUMBER:timestamp} %{IP:client_ip} %{WORD:method} %{URIPATHPARAM:request_url} %{NUMBER:status:int} %{NUMBER:request_time:float} (?:%{NUMBER:upstream_response_time:float}|-)`

// TestBuildLogstashFilterWithTag 单 pattern + tag：与手写习惯一致的结构（字符串写法、两级缩进）。
func TestBuildLogstashFilterWithTag(t *testing.T) {
	got := buildLogstashFilter("nginx", "message", []string{logstashSamplePattern},
		[]grokGsub{{Field: "upstream_response_time", From: "-", To: "0.0"}},
		[]string{"agent", "event"})
	want := `filter {
  if "nginx" in [tags] {
    grok {
      match => { "message" => "` + logstashSamplePattern + `" }
    }
    mutate {
      gsub => ["upstream_response_time", "-", "0.0"]
      remove_field => ["agent", "event"]
    }
  }
}
`
	if got != want {
		t.Fatalf("单 pattern 配置与预期不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuildLogstashFilterMultiPattern 多条 pattern 必须写成数组（写成单字符串时 logstash 会把
// 后面的整条当注释，静默只留第一条）。
func TestBuildLogstashFilterMultiPattern(t *testing.T) {
	got := buildLogstashFilter("nginx", "message", []string{"%{WORD:a}", "%{WORD:b}"}, nil, nil)
	want := `filter {
  if "nginx" in [tags] {
    grok {
      match => { "message" => [
        "%{WORD:a}",
        "%{WORD:b}"
      ] }
    }
  }
}
`
	if got != want {
		t.Fatalf("多 pattern 配置与预期不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuildLogstashFilterNoTag tag 留空时不加 if 包裹，内层缩进少一级。
func TestBuildLogstashFilterNoTag(t *testing.T) {
	got := buildLogstashFilter("", "", []string{"%{WORD:a}"}, nil, nil)
	want := `filter {
  grok {
    match => { "message" => "%{WORD:a}" }
  }
}
`
	if got != want {
		t.Fatalf("无 tag 配置与预期不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuildLogstashFilterNoMutate 没有可处理项时不能生成空 mutate 块（空块在 logstash 里是噪音）。
// 注意「三元组不完整」这类过滤是 sanitizeGsub 的职责（拼装器假设输入已校验过），
// 所以这里只测真正的空输入。
func TestBuildLogstashFilterNoMutate(t *testing.T) {
	cases := []struct {
		gsub   []grokGsub
		remove []string
	}{
		{nil, nil},
		{[]grokGsub{}, []string{}},
	}
	for _, c := range cases {
		got := buildLogstashFilter("", "", []string{"%{WORD:a}"}, c.gsub, c.remove)
		if strings.Contains(got, "mutate") {
			t.Fatalf("无可处理项时不应生成 mutate 块: %s", got)
		}
	}
	// 只有 gsub 不完整、remove 为空 —— 说明文字（notes）本来就不进配置，这里一并钉住
	if got := buildLogstashFilter("", "", []string{"%{WORD:a}"}, nil, nil); strings.Contains(got, "notes") {
		t.Fatalf("notes 是给界面看的，不该进配置: %s", got)
	}
}

// TestLogstashQuoteKeepsBackslash pattern 里的反斜杠必须原样保留：\d / \s / \[ 是要传进正则的，
// 转义成 \\d 会让 grok 匹配「字面反斜杠 + d」——配置语法没坏，运行起来一条都解析不出来。
func TestLogstashQuoteKeepsBackslash(t *testing.T) {
	got := buildLogstashFilter("", "", []string{`\[%{DATA:x}\] \d+`}, nil, nil)
	if !strings.Contains(got, `"\[%{DATA:x}\] \d+"`) {
		t.Fatalf("pattern 的反斜杠被改动过（logstash 侧会失配）: %s", got)
	}
	if logstashQuote(`a"b`) != `"a\"b"` {
		t.Fatalf("双引号必须转义，否则配置字符串被截断: %s", logstashQuote(`a"b`))
	}
}

// ---------------- 纯函数：字段清单与校验 ----------------

// TestPatternFields 字段清单 = 静态扫描 ∪ 样例实跑。
// 组合 pattern（%{COMBINEDAPACHELOG}）内部字段静态扫不到，必须靠实跑展开补齐。
func TestPatternFields(t *testing.T) {
	fields := patternFields([]string{logstashSamplePattern}, nil, nil)
	want := []string{"time", "timestamp", "client_ip", "method", "request_url", "status", "request_time", "upstream_response_time"}
	if strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Fatalf("字段清单不符（顺序应为 pattern 书写顺序）:\n got=%v\nwant=%v", fields, want)
	}
	// 重复字段只留一次
	dup := patternFields([]string{"%{WORD:a} %{WORD:a}"}, nil, nil)
	if len(dup) != 1 || dup[0] != "a" {
		t.Fatalf("重复字段应去重: %v", dup)
	}
	// 组合 pattern：静态扫不出内部字段，靠样例把值跑出来（COMBINEDAPACHELOG 需要 referrer / agent 两段）
	sample := `127.0.0.1 - - [10/Oct/2026:14:20:31 +0800] "GET /a HTTP/1.1" 200 123 "-" "curl/8.0"`
	combo := patternFields([]string{"%{COMBINEDAPACHELOG}"}, nil, []string{sample})
	if len(combo) == 0 {
		t.Fatalf("组合 pattern 的字段应通过样例实跑补齐，实际为空")
	}
	// 编译器本身认这条组合 pattern —— 若上游改名，上面那条会成为无声空断言，这里钉住前提
	if _, err := grok.Cached([]string{"%{COMBINEDAPACHELOG}"}, nil); err != nil {
		t.Fatalf("前提失效：内置 pattern 里没有 COMBINEDAPACHELOG: %v", err)
	}
}

// TestSanitizeGsub 模型给的 gsub 必须过本地校验：
// 替换一个 pattern 里根本不存在的字段纯属噪音，丢弃并回报（静默吞掉会让用户以为模型提过了）。
func TestSanitizeGsub(t *testing.T) {
	fields := []string{"time", "status", "upstream_response_time"}
	got, dropped := sanitizeGsub([]grokGsub{
		{Field: "upstream_response_time", From: "-", To: "0.0"},
		{Field: "ghost_field", From: "-", To: "0"},            // 幻觉字段
		{Field: "upstream_response_time", From: "x", To: "y"}, // 重复，留第一条
		{Field: "time", From: ""},                             // 三元组不完整
	}, fields)
	if len(got) != 1 || got[0].Field != "upstream_response_time" || got[0].To != "0.0" {
		t.Fatalf("gsub 过滤结果不符: %+v", got)
	}
	if len(dropped) != 1 || dropped[0] != "ghost_field" {
		t.Fatalf("幻觉字段应被丢弃并回报: %v", dropped)
	}
	if g, d := sanitizeGsub(nil, fields); len(g) != 0 || len(d) != 0 {
		t.Fatalf("无建议时不应产生任何输出: %v %v", g, d)
	}
}

// TestLogstashRemoveList 要删的字段由「开关 + 该连接的系统自带字段清单」共同决定，不走模型。
//
//   - 开关打开：清单去重后生效，并**始终带上 message**；
//   - 开关关闭：只删 message（原始整行已被 grok 拆开）；
//   - 清单为空：也只留 message —— 空的原因要么是还没探测过、要么是这集群确实没有可删的
//     元数据字段，两种情况下硬塞一组名字都是噪音（handler 在缓存未就绪时会退回候选池）。
func TestLogstashRemoveList(t *testing.T) {
	meta := []string{"agent", "host", "log", "message", "host"} // 故意含重复
	on := logstashRemoveList(true, meta)
	want := "agent,host,log,message"
	if strings.Join(on, ",") != want {
		t.Fatalf("开启时应去重保序并带上 message:\n got=%v\nwant=%s", on, want)
	}
	// 清单里没有 message 也要补上：所有 pattern 都把整行拆开了，留着就是一份重复数据
	if on2 := logstashRemoveList(true, []string{"agent", "log"}); !containsStr(on2, "message") {
		t.Fatalf("message 必须始终在删除列表里: %v", on2)
	}
	if empty := logstashRemoveList(true, nil); len(empty) != 1 || empty[0] != "message" {
		t.Fatalf("清单为空时应只删 message: %v", empty)
	}
	off := logstashRemoveList(false, []string{"agent", "host"})
	if len(off) != 1 || off[0] != "message" {
		t.Fatalf("关掉后仍要删 message（原始整行已被拆分）: %v", off)
	}
	// 必须返回副本：调用方改到返回值不能污染常量清单（否则后续请求跟着变）
	off[0] = "polluted"
	if logstashRemoveList(false, nil)[0] != "message" {
		t.Fatalf("logstashRemoveList 必须返回副本，不能把常量清单交出去")
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestNormalizeTagOrSource tag / source 直接进配置文本，非法字符（引号、换行、空白）必须拒收——
// 放进去会得到「语法坏了但不报错」的配置。
func TestNormalizeTagOrSource(t *testing.T) {
	if v, msg := normalizeTagOrSource(" nginx ", "tag", grokLogstashTagMax); v != "nginx" || msg != "" {
		t.Fatalf("正常值应去空白并通过: %q %q", v, msg)
	}
	if v, msg := normalizeTagOrSource("", "tag", grokLogstashTagMax); v != "" || msg != "" {
		t.Fatalf("空值应放行（表示不判 tag）: %q %q", v, msg)
	}
	for _, bad := range []string{`ngi"nx`, `ngi\nnx`, "nginx log", `a\b`} {
		if _, msg := normalizeTagOrSource(bad, "tag", grokLogstashTagMax); msg == "" {
			t.Fatalf("非法值必须拒收: %q", bad)
		}
	}
	if _, msg := normalizeTagOrSource(strings.Repeat("x", grokLogstashTagMax+1), "tag", grokLogstashTagMax); msg == "" {
		t.Fatalf("超长值必须拒收")
	}
}

// ---------------- HTTP 端点 ----------------

// fakeChatServer 非流式应答的 mock（配置生成走 Chat 而非 ChatStream）。
func fakeChatServer(t *testing.T, contents []string) (*httptest.Server, *aiCallRecorder) {
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
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"model":"grok-mock","choices":[{"message":{"role":"assistant","content":%s}},{"message":{"content":""}}],"usage":{"prompt_tokens":30,"completion_tokens":10,"total_tokens":40}}`, strconv.Quote(c))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// logstashReq 构造一条最简请求（样例能整行命中 logstashSamplePattern）。
func logstashReq(body map[string]any) map[string]any {
	sample := "2026-10-10 14:20:31 1728100000 10.0.0.1 GET /api/order 200 0.012 0.03"
	base := map[string]any{
		"patterns": []string{logstashSamplePattern},
		"tag":      "nginx",
		"source":   "message",
		"sample":   sample,
	}
	for k, v := range body {
		base[k] = v
	}
	return base
}

// TestGrokLogstashLocalOnly AI 未启用：配置照常给出（只有 grok 块），enhance=skipped 而不是失败。
func TestGrokLogstashLocalOnly(t *testing.T) {
	r, id := grokSetup(t, "")
	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	if out["code"] != float64(0) {
		t.Fatalf("AI 未启用不该让配置生成失败: %v", out)
	}
	data := out["data"].(map[string]any)
	cfg, _ := data["config"].(string)
	if !strings.Contains(cfg, `if "nginx" in [tags] {`) || !strings.Contains(cfg, "grok {") {
		t.Fatalf("配置骨架必须包含 tag 条件与 grok 块:\n%s", cfg)
	}
	// 开关默认打开 ⇒ 即便没有 AI，也要有 remove_field。内容来自字段缓存的兜底候选池
	//（测试环境没注入 fieldRepo；真机上是探测出来的清单 + 人工补充）
	if !strings.Contains(cfg, "remove_field => [") {
		t.Fatalf("默认应生成 remove_field:\n%s", cfg)
	}
	for _, want := range []string{`"agent"`, `"host"`, `"message"`} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("兜底候选池里应有 %s:\n%s", want, cfg)
		}
	}
	if data["enhance"] != grokEnhanceSkipped {
		t.Fatalf("AI 未启用应标 skipped（不是 failed，界面上不该报成错误）: %v", data["enhance"])
	}
	fields, _ := data["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("响应应带回 pattern 产出的字段清单")
	}
}

// TestGrokLogstashWithAI 模型给出 gsub / remove_field → 拼进 mutate 块，状态为 ok。
func TestGrokLogstashWithAI(t *testing.T) {
	srv, rec := fakeChatServer(t, []string{
		`{"gsub":[{"field":"upstream_response_time","from":"-","to":"0.0"}],"remove_field":["agent","event","host"],"notes":"占位符归零并清理采集器字段"}`,
	})
	r, id := grokSetup(t, srv.URL)
	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	data := out["data"].(map[string]any)
	if out["code"] != float64(0) || data["enhance"] != grokEnhanceOK {
		t.Fatalf("AI 增强应成功: %v", out)
	}
	cfg, _ := data["config"].(string)
	if !strings.Contains(cfg, `gsub => ["upstream_response_time", "-", "0.0"]`) {
		t.Fatalf("配置缺少模型给的 gsub:\n%s", cfg)
	}
	// remove_field 与模型无关：内容来自「开关 + 字段缓存」（此处是缓存未注入时的兜底候选池）
	if !strings.Contains(cfg, "remove_field => [") {
		t.Fatalf("配置缺少 remove_field:\n%s", cfg)
	}
	if rec.count() != 1 {
		t.Fatalf("应只调用模型一次（配置生成不做重试）: %d", rec.count())
	}
	// 提示词里必须带上字段清单与样例，否则模型只能瞎猜
	last := rec.at(0)
	if !strings.Contains(last[1].Content, "client_ip") || !strings.Contains(last[1].Content, "10.0.0.1") {
		t.Fatalf("提示词应包含字段清单与样例日志")
	}
}

// TestGrokLogstashAIHallucination 模型即使自己塞了 remove_field 也不影响结果：
// 删哪些字段是按「开关 + 字段缓存」算出来的，模型不参与（提示词也让明确它别管）。
// 这条防的是「哪天有人又把 remove_field 接回模型建议」——那会把业务字段写进删除列表，
// 而且不报错、只丢数据。
func TestGrokLogstashAIHallucination(t *testing.T) {
	srv, _ := fakeChatServer(t, []string{
		`{"gsub":[{"field":"not_a_field","from":"-","to":"0"}],"remove_field":["status","agent","evil_field"]}`,
	})
	r, id := grokSetup(t, srv.URL)
	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	data := out["data"].(map[string]any)
	cfg, _ := data["config"].(string)
	if strings.Contains(cfg, `"status"`) {
		t.Fatalf("业务字段绝不能进 remove_field:\n%s", cfg)
	}
	for _, ghost := range []string{"not_a_field", "evil_field"} {
		if strings.Contains(cfg, ghost) {
			t.Fatalf("模型给的东西不该出现在配置里（清单不走模型）: %s\n%s", ghost, cfg)
		}
	}
	if !strings.Contains(cfg, "remove_field => [") || !strings.Contains(cfg, `"agent"`) {
		t.Fatalf("remove_field 应来自字段缓存的兜底候选池:\n%s", cfg)
	}
	dg, _ := data["dropped_gsub"].([]any)
	if len(dg) != 1 || dg[0] != "not_a_field" {
		t.Fatalf("幻觉 gsub 字段要回报给前端: %v", dg)
	}
}

// TestGrokLogstashAIFailed AI 启用了但调用失败：配置仍要完整可用，状态标记为 failed。
func TestGrokLogstashAIFailed(t *testing.T) {
	srv, _ := fakeChatServer(t, []string{"{}"})
	url := srv.URL
	srv.Close() // 端口已关 → 连接失败
	r, id := grokSetup(t, url)
	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	data := out["data"].(map[string]any)
	if out["code"] != float64(0) {
		t.Fatalf("AI 失败不该让配置生成失败: %v", out)
	}
	cfg, _ := data["config"].(string)
	if !strings.Contains(cfg, "grok {") {
		t.Fatalf("降级后仍必须给出可用的纯解析配置:\n%s", cfg)
	}
	if data["enhance"] != grokEnhanceFailed {
		t.Fatalf("应标 failed: %v", data["enhance"])
	}
	if s, _ := data["enhance_error"].(string); s == "" {
		t.Fatalf("failed 时要给出原因")
	}
}

// TestGrokLogstashBadInput 输入非法必须报 4016（前端会把 message 显示在面板上）。
func TestGrokLogstashBadInput(t *testing.T) {
	r, id := grokSetup(t, "")
	path := fmt.Sprintf("/api/es/%d/grok/logstash", id)
	cases := []struct {
		name string
		body map[string]any
		hint string
	}{
		{"空 pattern", map[string]any{"patterns": []string{}, "tag": "nginx"}, "pattern"},
		{"pattern 编译失败", map[string]any{"patterns": []string{"%{NOPE:x}"}, "tag": "nginx"}, "未定义"},
		{"tag 含引号", logstashReq(map[string]any{"tag": `a"b`}), "引号"},
		{"source 含换行", logstashReq(map[string]any{"source": "a\nb"}), "引号"},
	}
	for _, c := range cases {
		_, out := grokPost(t, r, path, c.body)
		if out["code"] != float64(esCodeGrokInvalid) {
			t.Fatalf("%s: 期望错误码 %d，实际 %v", c.name, esCodeGrokInvalid, out)
		}
		if s, _ := out["message"].(string); !strings.Contains(s, c.hint) {
			t.Fatalf("%s: 错误信息应说明原因（含 %q），实际 %q", c.name, c.hint, s)
		}
	}
}

// TestGrokLogstashStreams 这个端点**必须走 SSE**：一次模型调用几秒到几十秒，远超 axios 的
// 15 秒默认超时，同步返回的表现是「每次点生成都报请求超时 · POST /es/:id/grok/logstash」（实测踩到）。
// 同步与流式返回对上面那些断言是等价的（grokPost 两种都能解），所以只有直接检查响应头才锁得住。
// 顺带锁住「有增强时先推一帧 phase」——按钮干转十几秒而界面没有任何说明，用户只会以为卡死了。
func TestGrokLogstashStreams(t *testing.T) {
	srv, _ := fakeChatServer(t, []string{`{"gsub":[],"remove_field":["agent"],"notes":""}`})
	r, id := grokSetup(t, srv.URL)
	w, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("必须走 SSE（同步返回会被 axios 的 15 秒默认超时打断）: %q", ct)
	}
	dump := w.Body.String()
	if !strings.Contains(dump, "event:final") {
		t.Fatalf("缺少 final 帧: %s", dump)
	}
	if !strings.Contains(dump, "event:phase") {
		t.Fatalf("有增强时应先推一帧进度（否则盒子只能干转十几秒）: %s", dump)
	}
	if out["code"] != float64(0) {
		t.Fatalf("final 帧应带正常结果: %v", out)
	}

}

// TestGrokLogstashNoPhaseWithoutAI 未配 AI 时不该推 phase 帧：那种情况立刻返回，
// 闪一帧「正在判断」反而像出了错；但 final 帧必须有（配置照常给）。
// 单独一个用例而不是塞进上面那个：一次测试只能建一套库，同一用例里调两次 grokSetup
// 会让临时目录清理时撞上「数据库文件仍被占用」（实测）。
func TestGrokLogstashNoPhaseWithoutAI(t *testing.T) {
	r, id := grokSetup(t, "")
	dump := grokPost2(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	if strings.Contains(dump, "event:phase") {
		t.Fatalf("未配 AI 时不该推 phase 帧: %s", dump)
	}
	if !strings.Contains(dump, "event:final") {
		t.Fatalf("未配 AI 时也要有 final 帧（配置照常给）: %s", dump)
	}
}

// grokPost2 只要原始响应体（上面的用例要检查帧序列本身，不只要解出来的信封）。
func grokPost2(t *testing.T, r *gin.Engine, path string, body any) string {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	return w.Body.String()
}
