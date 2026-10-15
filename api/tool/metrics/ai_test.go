package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func containsHint(hints []string, want string) bool {
	for _, h := range hints {
		if h == want {
			return true
		}
	}
	return false
}

func TestAlignHints(t *testing.T) {
	cat := &cachedCatalog{
		labels: []string{"project", "instance", "container"},
		values: map[string][]string{
			"project":   {"成享花", "云汉"},
			"instance":  {"web-01:9100", "10.0.0.2:9100"},
			"container": {"jxh-app"},
		},
	}
	// 中文项目名：取值整体出现在问题里（①），同时 "app" 反向命中 container 值（②）
	hints := alignHints("我想查询今天成享花项目的app容器的内存使用情况", cat)
	if !containsHint(hints, `「成享花」→ project="成享花"`) {
		t.Errorf("hints 缺少中文项目对齐: %v", hints)
	}
	if !containsHint(hints, `「jxh-app」→ container="jxh-app"`) {
		t.Errorf("hints 缺少容器前缀对齐: %v", hints)
	}
	// 英文前缀：问题词是取值的一部分（web-01 ↔ web-01:9100）
	hints = alignHints("看下 web-01 这台机器的内存", cat)
	if !containsHint(hints, `「web-01:9100」→ instance="web-01:9100"`) {
		t.Errorf("hints 缺少实例前缀对齐: %v", hints)
	}
	// 无实体词：不产出对齐
	if hints = alignHints("查看各节点内存使用率的变化", cat); len(hints) != 0 {
		t.Errorf("无实体词不应产出对齐: %v", hints)
	}
}

func TestLabelsBlock(t *testing.T) {
	pods := make([]string, 50)
	for i := range pods {
		pods[i] = fmt.Sprintf("pod-%02d", i)
	}
	cat := &cachedCatalog{
		labels: []string{"project", "podName"},
		values: map[string][]string{"project": {"成享花", "云汉"}, "podName": pods},
		totals: map[string]int{"project": 2, "podName": 500},
	}
	blk := labelsBlock(cat, "成享花的 app 容器内存")
	if !strings.Contains(blk, "本集群可用标签：project、podName") {
		t.Errorf("缺少标签全集: %q", blk)
	}
	if !strings.Contains(blk, "project（共 2 个）：成享花, 云汉") {
		t.Errorf("缺少项目取值: %q", blk)
	}
	// 单标签列表截到 labelCtxMaxValues(40) 个，并附总数与省略号
	if !strings.Contains(blk, "podName（共 500 个）：pod-00") || !strings.Contains(blk, "pod-39") {
		t.Errorf("podName 取值截断异常: %q", blk)
	}
	if strings.Contains(blk, "pod-40") || !strings.Contains(blk, " …") {
		t.Errorf("podName 超出 40 个应截断并标注省略: %q", blk)
	}
	if !strings.Contains(blk, `「成享花」→ project="成享花"`) {
		t.Errorf("缺实体对齐提示: %q", blk)
	}
	// 无标签信息（远端不支持）时返回空串，生成退回旧样
	empty := labelsBlock(&cachedCatalog{}, "任意问题")
	if empty != "" {
		t.Errorf("无标签时应返回空串, got %q", empty)
	}
}

func TestFetchLabelValues(t *testing.T) {
	srv := fakeProm(t)
	defer srv.Close()
	c, err := newPromClient(srv.URL, false, "none", "", "")
	if err != nil {
		t.Fatalf("newPromClient: %v", err)
	}
	labels, err := c.labelNames(context.Background())
	if err != nil || len(labels) != 2 {
		t.Fatalf("labelNames = %v, err = %v", labels, err)
	}
	values, totals := fetchLabelValues(context.Background(), c, append(labels, "missing"))
	if len(values["project"]) != 2 || totals["project"] != 2 || values["project"][0] != "成享花" {
		t.Fatalf("project = %v totals = %v", values["project"], totals)
	}
	if len(values["job"]) != 1 || values["job"][0] != "agent" {
		t.Fatalf("job = %v", values["job"])
	}
	if _, ok := values["missing"]; ok {
		t.Errorf("探测失败的标签应被放弃")
	}
}

// TestResolveAIRange 锁定时间范围标记的换算口径：滚动窗口（last_Nm/Nh/Nd）与
// 日历边界（今天 / 昨天 / 本周 / 上周 / 本月 / 上月），以及不认识标记的放弃。
func TestResolveAIRange(t *testing.T) {
	// 固定基准：2026-10-09（周五）14:30 本地时间
	now := time.Date(2026, 10, 9, 14, 30, 0, 0, time.Local)
	ms := func(tm time.Time) int64 { return tm.UnixMilli() }
	mid := time.Date(2026, 10, 9, 0, 0, 0, 0, time.Local)
	mon := time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local) // 本周一
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)

	cases := []struct {
		mark   string
		start  int64
		end    int64
		label  string
		preset string
	}{
		{"last_30m", ms(now.Add(-30 * time.Minute)), ms(now), "最近 30 分钟", ""},
		{"last_6h", ms(now.Add(-6 * time.Hour)), ms(now), "最近 6 小时", "6h"},
		{"last_1d", ms(now.Add(-24 * time.Hour)), ms(now), "最近 1 天", "24h"},
		{"last_3d", ms(now.AddDate(0, 0, -3)), ms(now), "最近 3 天", ""},
		{"last_7d", ms(now.AddDate(0, 0, -7)), ms(now), "最近 7 天", "7d"},
		{"today", ms(mid), ms(now), "今天", ""},
		{"yesterday", ms(mid.AddDate(0, 0, -1)), ms(mid), "昨天", ""},
		{"this_week", ms(mon), ms(now), "本周", ""},
		{"last_week", ms(mon.AddDate(0, 0, -7)), ms(mon), "上周", ""},
		{"this_month", ms(month), ms(now), "本月", ""},
		{"last_month", ms(month.AddDate(0, -1, 0)), ms(month), "上月", ""},
	}
	for _, c := range cases {
		got := resolveAIRange(c.mark, now)
		if got == nil {
			t.Errorf("%s：应换算出区间，得到 nil", c.mark)
			continue
		}
		if got.Start != c.start || got.End != c.end || got.Label != c.label || got.Preset != c.preset {
			t.Errorf("%s：got start=%d end=%d label=%q preset=%q，want start=%d end=%d label=%q preset=%q",
				c.mark, got.Start, got.End, got.Label, got.Preset, c.start, c.end, c.label, c.preset)
		}
	}

	// 大小写 / 空白宽容
	if got := resolveAIRange("  TODAY  ", now); got == nil || got.Label != "今天" {
		t.Errorf("标记应大小写 / 空白宽容: %+v", got)
	}
	// 空、不认识、越界的标记 → nil（前端保持用户当前选择，不猜）
	for _, m := range []string{"", "   ", "上周五", "last_0m", "last_365d", "last_1y", "最近3天"} {
		if got := resolveAIRange(m, now); got != nil {
			t.Errorf("%q 不应换算区间，得到 %+v", m, got)
		}
	}
}

// TestBuildFollowupMessages 锁定追加生成的对话拼装：只带上一轮（assistant 上一版 + user 追加要求），
// 上一版缺失时退回单轮生成，并保证追加要求里写明「range 沿用上一轮」。
func TestBuildFollowupMessages(t *testing.T) {
	// 没有上一轮 / 上一轮没有表达式 → 不产出上下文（调用方按单轮处理）
	if got := buildFollowupMessages(nil, "再看内存"); got != nil {
		t.Errorf("没有上一轮时应返回 nil，得到 %+v", got)
	}
	if got := buildFollowupMessages(&aiPromQLPrev{Question: "查 CPU"}, "再看内存"); got != nil {
		t.Errorf("上一轮没有表达式时应返回 nil，得到 %+v", got)
	}

	msgs := buildFollowupMessages(&aiPromQLPrev{
		Question: "今天各容器内存",
		PromQL:   `container_memory_usage{namespace="scfq-service"}`,
		Explain:  "容器内存",
	}, "以容器名称维度展示")
	if len(msgs) != 2 {
		t.Fatalf("追加上下文应为 2 条消息，得到 %d", len(msgs))
	}
	if msgs[0].Role != "assistant" || msgs[1].Role != "user" {
		t.Fatalf("消息角色错位: %s / %s", msgs[0].Role, msgs[1].Role)
	}
	// assistant 那一条必须是可解析的上一版 JSON（模型据此看到上一版表达式）
	var prev promqlReply
	if err := unmarshalModelJSON(msgs[0].Content, &prev); err != nil {
		t.Fatalf("上一版 JSON 不可解析: %v（%s）", err, msgs[0].Content)
	}
	if prev.PromQL != `container_memory_usage{namespace="scfq-service"}` {
		t.Errorf("上一版表达式丢失: %q", prev.PromQL)
	}
	// 追加要求段要带上：本轮要求 + range 沿用规则（否则用户识别出的时间范围会被清空）
	if !strings.Contains(msgs[1].Content, "以容器名称维度展示") {
		t.Errorf("追加要求段缺少本轮要求: %s", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "range 沿用上一轮") {
		t.Errorf("追加要求段缺少 range 沿用规则: %s", msgs[1].Content)
	}
}

// TestTraceQuestion 调用记录里追加生成要能与首轮区分开。
func TestTraceQuestion(t *testing.T) {
	if got := traceQuestion("查内存", false); got != "查内存" {
		t.Errorf("首轮问题不应加前缀: %q", got)
	}
	if got := traceQuestion("再看内存", true); got != "追加：再看内存" {
		t.Errorf("追加问题应带前缀: %q", got)
	}
}
