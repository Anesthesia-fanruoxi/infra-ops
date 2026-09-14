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
