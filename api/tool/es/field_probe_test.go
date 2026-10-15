// field_probe_test.go 系统自带字段的探测与缓存：三层过滤、两种降级、以及「探测不许动人填的补充」。
//
// 这几条都是「不报错但结果错」的类型：少一层过滤就会删掉业务字段或不存在的字段，
// 探测覆盖 extra 就会把用户手填的 offset / logYear 抹掉 —— 只能在测试里钉住。
package es

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infra-ops/store/repo"
)

// fakeProbeES 探测用到的三个接口的最小假集群。
// ecsTop / live 传 nil 表示该接口不可用（分别是「没有 ECS 组件模板」与「集群里没有任何索引」）。
type fakeProbeES struct {
	version string
	ecsTop  []string
	live    []string
}

func newFakeProbeClient(t *testing.T, f fakeProbeES) *escClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			probeWriteJSON(w, 200, map[string]any{"version": map[string]any{"number": f.version}})
		case strings.HasPrefix(r.URL.Path, "/_component_template/"):
			if f.ecsTop == nil {
				probeWriteJSON(w, 404, map[string]any{"error": "no such component template"})
				return
			}
			props := map[string]any{}
			for _, k := range f.ecsTop {
				props[k] = map[string]any{"properties": map[string]any{}}
			}
			probeWriteJSON(w, 200, map[string]any{"component_templates": []any{map[string]any{
				"name": "ecs@mappings",
				"component_template": map[string]any{
					"template": map[string]any{"mappings": map[string]any{"properties": props}},
				},
			}}})
		case strings.HasPrefix(r.URL.Path, "/_field_caps"):
			if f.live == nil {
				probeWriteJSON(w, 404, map[string]any{"error": "no indices"})
				return
			}
			fields := map[string]any{}
			for _, k := range f.live {
				fields[k] = map[string]any{"keyword": map[string]any{"type": "keyword"}}
			}
			probeWriteJSON(w, 200, map[string]any{"fields": fields})
		default:
			probeWriteJSON(w, 404, map[string]any{"error": "unexpected path " + r.URL.Path})
		}
	}))
	t.Cleanup(srv.Close)
	client, err := newESCClient(srv.URL, false, "anonymous", "", "")
	if err != nil {
		t.Fatalf("建客户端失败: %v", err)
	}
	return client
}

// probeWriteJSON 名字带前缀：包内其他测试文件已有同名助手的教训（重名即编译失败）。
func probeWriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// TestProbeDefaultFields 三层过滤：候选池 ∩ 该版本 ECS 定义 ∩ 集群里真实存在。三层缺一不可。
func TestProbeDefaultFields(t *testing.T) {
	client := newFakeProbeClient(t, fakeProbeES{
		version: "8.13.0",
		// ECS 定义里既有元数据（agent/host/log）也有业务对象（url/http/message）
		ecsTop: []string{"agent", "cloud", "container", "data_stream", "ecs", "event",
			"host", "input", "log", "message", "url", "http"},
		// 集群里真实采到的：元数据 + 业务字段，另外还有嵌套路径 host.name
		live: []string{"agent", "host", "log", "message", "url", "http", "host.name"},
	})
	version, fields, status, msg := probeESDefaultFields(context.Background(), client)
	if version != "8.13.0" || status != esFieldStatusOK || msg != "" {
		t.Fatalf("版本/状态/说明不符: %q %q %q", version, status, msg)
	}
	if got, want := strings.Join(fields, ","), "agent,host,log"; got != want {
		t.Fatalf("三层过滤结果不符:\n got=%s\nwant=%s", got, want)
	}
	// 业务对象即使 ECS 里有、集群里也有，也不能进清单（否则就是删业务数据）
	for _, bad := range []string{"url", "http", "message"} {
		if containsStr(fields, bad) {
			t.Fatalf("业务字段不该进删除清单: %s（%v）", bad, fields)
		}
	}
	// ECS 有但集群没采到的也不该进（删不存在的字段纯属噪音）
	if containsStr(fields, "data_stream") || containsStr(fields, "cloud") {
		t.Fatalf("集群里不存在的字段不该进清单: %v", fields)
	}
}

// TestProbeDefaultFieldsNoECS 低版本 / 权限不足导致拿不到 ECS 组件模板时：
// 退化为「候选池 ∩ 实际存在」并标 no_ecs，不能因为探测不到就把功能卡住。
func TestProbeDefaultFieldsNoECS(t *testing.T) {
	client := newFakeProbeClient(t, fakeProbeES{
		version: "7.10.0", ecsTop: nil,
		live: []string{"agent", "host", "server"},
	})
	_, fields, status, msg := probeESDefaultFields(context.Background(), client)
	if status != esFieldStatusNoECS {
		t.Fatalf("拿不到 ECS 定义时应标 no_ecs: %q", status)
	}
	if !strings.Contains(msg, "ECS") {
		t.Fatalf("no_ecs 要说明原因，否则用户看不懂清单为什么短: %q", msg)
	}
	if got := strings.Join(fields, ","); got != "agent,host" {
		t.Fatalf("应退化为「候选池 ∩ 实际存在」: %s", got)
	}
}

// TestProbeDefaultFieldsNoIndices 集群里一个索引都没有（_field_caps 404）：
// 跳过「实际存在」这层，保留 ECS 定义 ∩ 候选池，并在说明里讲清楚。
func TestProbeDefaultFieldsNoIndices(t *testing.T) {
	client := newFakeProbeClient(t, fakeProbeES{
		version: "8.13.0",
		ecsTop:  []string{"agent", "host", "log", "url"},
		live:    nil,
	})
	_, fields, status, msg := probeESDefaultFields(context.Background(), client)
	if status != esFieldStatusOK {
		t.Fatalf("空集群不是探测失败: %q", status)
	}
	if !strings.Contains(msg, "未能核对") {
		t.Fatalf("要说明这层被跳过了: %q", msg)
	}
	if got := strings.Join(fields, ","); got != "agent,host,log" {
		t.Fatalf("应保留 ECS 定义 ∩ 候选池: %s", got)
	}
}

// TestProbeDefaultFieldsUnreachable 集群连不上：标 failed 并带原因（界面要能回答「为什么是空的」）。
func TestProbeDefaultFieldsUnreachable(t *testing.T) {
	client, err := newESCClient("http://127.0.0.1:1", false, "anonymous", "", "")
	if err != nil {
		t.Fatalf("建客户端失败: %v", err)
	}
	_, fields, status, msg := probeESDefaultFields(context.Background(), client)
	if status != esFieldStatusFailed || msg == "" {
		t.Fatalf("连不上应标 failed 并带原因: %q %q", status, msg)
	}
	if len(fields) != 0 {
		t.Fatalf("探测失败不该编造清单: %v", fields)
	}
}

// TestESFieldRepoProbeKeepsExtra 探测整块覆盖 fields，但**绝不碰 extra**。
// extra 是人在界面上登记的补充清单（offset / logYear 这类 ECS 之外的字段），
// 一轮后台刷新把它抹掉，用户手填的东西就没了 —— 而且不会报任何错。
func TestESFieldRepoProbeKeepsExtra(t *testing.T) {
	_, connID := grokSetup(t, "")
	fr := repo.NewESFieldRepo()

	if err := fr.SaveExtra(connID, []string{"offset", "logYear"}); err != nil {
		t.Fatalf("存人工补充失败: %v", err)
	}
	// 第一轮探测
	if err := fr.SaveProbe(connID, "8.13.0", []string{"agent", "host"}, esFieldStatusOK, ""); err != nil {
		t.Fatalf("第一轮探测入库失败: %v", err)
	}
	// 第二轮探测（模拟后台周期刷新）：fields 变短了，extra 必须原样
	if err := fr.SaveProbe(connID, "8.13.0", []string{"agent"}, esFieldStatusOK, ""); err != nil {
		t.Fatalf("第二轮探测入库失败: %v", err)
	}
	c, err := fr.Get(connID)
	if err != nil || c == nil {
		t.Fatalf("读缓存失败: %v", err)
	}
	if strings.Join(c.Fields, ",") != "agent" {
		t.Fatalf("fields 应被最新一轮整体覆盖: %v", c.Fields)
	}
	if strings.Join(c.Extra, ",") != "offset,logYear" {
		t.Fatalf("extra 是人工数据，任何一轮探测都不许动: %v", c.Extra)
	}
	if c.ESVersion != "8.13.0" || c.Status != esFieldStatusOK {
		t.Fatalf("版本与状态要落库: %+v", c)
	}
}

// TestESFieldRepoGetMissing 没有记录时返回 (nil, nil) —— 那是「还没探测过」的正常状态，
// 不该让调用方去辨认 sql.ErrNoRows。
func TestESFieldRepoGetMissing(t *testing.T) {
	_, connID := grokSetup(t, "")
	fr := repo.NewESFieldRepo()
	c, err := fr.Get(connID)
	if err != nil || c != nil {
		t.Fatalf("未探测时应返回 (nil, nil): %v %v", c, err)
	}
}

// TestGrokLogstashUsesFieldCache 缓存里的清单真的进了配置：这是整套「按版本自动获取」的验收点。
// 探测结果（fields）与人工补充（extra）都要出现在 remove_field 里，且 message 始终在。
func TestGrokLogstashUsesFieldCache(t *testing.T) {
	r, id := grokSetup(t, "")
	fr := repo.NewESFieldRepo()
	if err := fr.SaveProbe(id, "8.13.0", []string{"agent", "host"}, esFieldStatusOK, ""); err != nil {
		t.Fatalf("写探测结果失败: %v", err)
	}
	if err := fr.SaveExtra(id, []string{"offset", "logYear", "logMillis"}); err != nil {
		t.Fatalf("写人工补充失败: %v", err)
	}

	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id), logstashReq(nil))
	data := out["data"].(map[string]any)
	cfg, _ := data["config"].(string)
	for _, want := range []string{"agent", "host", "offset", "logYear", "logMillis", "message"} {
		if !strings.Contains(cfg, `"`+want+`"`) {
			t.Fatalf("remove_field 应含 %s（探测结果 + 人工补充）:\n%s", want, cfg)
		}
	}
	// 缓存里没有的（如 ecs）不该凭空出现 —— 兜底只在缓存未就绪时发生
	if strings.Contains(cfg, `"ecs"`) {
		t.Fatalf("缓存里没有的字段不该出现在清单里:\n%s", cfg)
	}
	if data["es_version"] != "8.13.0" || data["cache_status"] != esFieldStatusOK {
		t.Fatalf("响应要带清单来源（版本 + 状态），界面据此说明: %v %v", data["es_version"], data["cache_status"])
	}
}

// TestGrokLogstashDropDefaultsOff 关掉开关：只删 message —— 那些自动字段留着，
// 但原始整行必须删（它已经被 grok 拆成字段了，留着就是一份重复数据）。
func TestGrokLogstashDropDefaultsOff(t *testing.T) {
	r, id := grokSetup(t, "")
	fr := repo.NewESFieldRepo()
	if err := fr.SaveProbe(id, "8.13.0", []string{"agent", "host"}, esFieldStatusOK, ""); err != nil {
		t.Fatalf("写探测结果失败: %v", err)
	}
	off := false
	_, out := grokPost(t, r, fmt.Sprintf("/api/es/%d/grok/logstash", id),
		logstashReq(map[string]any{"drop_defaults": off}))
	data := out["data"].(map[string]any)
	cfg, _ := data["config"].(string)
	if !strings.Contains(cfg, `remove_field => ["message"]`) {
		t.Fatalf("关掉开关后应只删 message:\n%s", cfg)
	}
	if strings.Contains(cfg, `"agent"`) {
		t.Fatalf("关掉开关后不该再删自动字段:\n%s", cfg)
	}
	if data["drop_defaults"] != false {
		t.Fatalf("响应要回显开关状态: %v", data["drop_defaults"])
	}
}
