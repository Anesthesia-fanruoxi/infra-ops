package es

import (
	"testing"

	"infra-ops/model"
)

func mkFields(items []model.ESViewField) []model.ESViewField { return items }

func df(path string) model.ESViewField {
	return model.ESViewField{Path: path, Types: []string{"date"}, Searchable: true, Aggregatable: true}
}

func TestPickTimeFieldsFromFields(t *testing.T) {
	fields := mkFields([]model.ESViewField{
		{Path: "host", Types: []string{"keyword"}},
		df("@timestamp"),
		df("created_at"),
		df("event_time"),
	})
	got := pickTimeFieldsFromFields(fields)
	if len(got) != 3 {
		t.Fatalf("应选出 3 个 date 字段，得到 %v", got)
	}
	if got[0] != "@timestamp" {
		t.Fatalf("@timestamp 应置顶，得到 %v", got)
	}
}

func TestResolveTimeFieldRules(t *testing.T) {
	fields := mkFields([]model.ESViewField{
		df("@timestamp"),
		{Path: "count", Types: []string{"text", "keyword"}, TypeConflict: true},
		{Path: "partial_f", Types: []string{"date"}, Partial: true},
		{Path: "name", Types: []string{"text"}},
	})

	t.Run("空 want 取候选首个", func(t *testing.T) {
		cc := &collateResult{Fields: fields, TimeCandidates: []string{"@timestamp"}}
		got, verr := resolveTimeField(cc, "")
		if verr != nil || got != "@timestamp" {
			t.Fatalf("期望 @timestamp，得到 %s / err=%v", got, verr)
		}
	})

	t.Run("无任何候选 → 4004", func(t *testing.T) {
		cc := &collateResult{Fields: mkFields([]model.ESViewField{{Path: "a", Types: []string{"keyword"}}})}
		_, verr := resolveTimeField(cc, "")
		if verr == nil || verr.code != esCodeNoTimeField {
			t.Fatalf("期望 4004，得到 %v", verr)
		}
	})

	t.Run("多类型冲突 → 4009", func(t *testing.T) {
		cc := &collateResult{Fields: fields}
		_, verr := resolveTimeField(cc, "count")
		if verr == nil || verr.code != esCodeTimeFieldConflict {
			t.Fatalf("期望 4009，得到 %v", verr)
		}
	})

	t.Run("部分成员缺失 → 4009", func(t *testing.T) {
		cc := &collateResult{Fields: fields}
		_, verr := resolveTimeField(cc, "partial_f")
		if verr == nil || verr.code != esCodeTimeFieldConflict {
			t.Fatalf("期望 4009，得到 %v", verr)
		}
	})

	t.Run("非 date 类型 → 4009", func(t *testing.T) {
		cc := &collateResult{Fields: fields}
		_, verr := resolveTimeField(cc, "name")
		if verr == nil || verr.code != esCodeTimeFieldConflict {
			t.Fatalf("期望 4009，得到 %v", verr)
		}
	})

	t.Run("指定字段不存在 → 4009", func(t *testing.T) {
		cc := &collateResult{Fields: fields}
		_, verr := resolveTimeField(cc, "no_such")
		if verr == nil || verr.code != esCodeTimeFieldConflict {
			t.Fatalf("期望 4009，得到 %v", verr)
		}
	})

	t.Run("正常指定 → 返回原值", func(t *testing.T) {
		cc := &collateResult{Fields: fields}
		got, verr := resolveTimeField(cc, "@timestamp")
		if verr != nil || got != "@timestamp" {
			t.Fatalf("期望 @timestamp，得到 %s / err=%v", got, verr)
		}
	})
}

func TestNormalizeIndexPattern(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ysh", "ysh*"},
		{"ysh*", "ysh*"},
		{"logs-?", "logs-?"},
		{"ysh,nginx", "ysh*,nginx*"},
		{"logs-*,ysh", "logs-*,ysh*"},
		{" ysh , nginx ", "ysh*,nginx*"},
		{"ysh,,nginx", "ysh*,nginx*"},
		{"", ""},
		{" , ", ""},
	}
	for _, tc := range cases {
		if got := normalizeIndexPattern(tc.in); got != tc.want {
			t.Errorf("normalizeIndexPattern(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func strPtr(s string) *string { return &s }

// TestPlanViewUpdate 覆盖「编辑只提交变更字段」的归算：这是「只改名/不改时间字段时报
// 400 时间字段变更需同时提供 index_pattern」那个缺陷的定点回归。
func TestPlanViewUpdate(t *testing.T) {
	cur := &model.ESView{Name: "nginx", IndexPattern: "nginx-*", TimeField: "@timestamp"}

	t.Run("空请求：全部沿用当前值且不重探", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if got.Name != cur.Name || got.Pattern != cur.IndexPattern || got.TimeField != cur.TimeField || got.NeedProbe {
			t.Fatalf("归算错误: %+v", got)
		}
	})

	t.Run("只改名：不动 pattern、不重探", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{Name: strPtr("web")})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if got.Name != "web" || got.Pattern != "nginx-*" || got.NeedProbe {
			t.Fatalf("归算错误: %+v", got)
		}
	})

	t.Run("改名传空白串：沿用当前名", func(t *testing.T) {
		got, _ := planViewUpdate(cur, viewUpdateReq{Name: strPtr("   ")})
		if got.Name != "nginx" {
			t.Fatalf("空白名应沿用当前值，得到 %q", got.Name)
		}
	})

	t.Run("时间字段原值等同当前：不重探（缺陷点）", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{TimeField: strPtr("@timestamp")})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if !got.TimeProvided || got.NeedProbe {
			t.Fatalf("值未变化不应重探: %+v", got)
		}
	})

	t.Run("时间字段换成新值：重探", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{TimeField: strPtr("event_time")})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if !got.NeedProbe || got.TimeWant != "event_time" {
			t.Fatalf("归算错误: %+v", got)
		}
	})

	t.Run("显式清空时间字段：重探且重新自动挑选", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{TimeField: strPtr("")})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if !got.NeedProbe || !got.TimeProvided || got.TimeWant != "" {
			t.Fatalf("归算错误: %+v", got)
		}
	})

	t.Run("裸 pattern 自动补 * 并重探", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{IndexPattern: strPtr("nginx")})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if got.Pattern != "nginx*" || !got.NeedProbe {
			t.Fatalf("归算错误: %+v", got)
		}
	})

	t.Run("补 * 后与当前等价：不重探", func(t *testing.T) {
		got, msg := planViewUpdate(cur, viewUpdateReq{IndexPattern: strPtr("nginx-*")})
		if msg != "" {
			t.Fatalf("不应报错: %s", msg)
		}
		if got.NeedProbe {
			t.Fatalf("等价 pattern 不应重探: %+v", got)
		}
	})

	t.Run("pattern 归一后为空：返回错误信息", func(t *testing.T) {
		if _, msg := planViewUpdate(cur, viewUpdateReq{IndexPattern: strPtr(",")}); msg == "" {
			t.Fatal("期望返回非法提示")
		}
	})
}
