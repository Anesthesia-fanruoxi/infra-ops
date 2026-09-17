package es

import (
	"testing"
)

func cap(typeName string, searchable, agg bool) capInfo {
	return capInfo{Type: typeName, Searchable: searchable, Aggregatable: agg}
}

// TestMergeFields_SameType 同名同类型：能力位保守取交（任一成员 false 即 false）。
func TestMergeFields_SameType(t *testing.T) {
	members := []map[string]map[string]capInfo{
		{"message": {"text": cap("text", true, false)}},
		{"message": {"text": cap("text", false, false)}},
	}
	fields, _ := mergeFields(members)
	if len(fields) != 1 {
		t.Fatalf("期望 1 个字段，得到 %d", len(fields))
	}
	f := fields[0]
	if f.TypeConflict {
		t.Errorf("同名同类型不应判为冲突")
	}
	if f.Partial {
		t.Errorf("两个成员都有该字段，不应 partial")
	}
	if f.Searchable {
		t.Errorf("能力位应取交（取 false），得到 searchable=true")
	}
	if f.Aggregatable {
		t.Errorf("两成员均 false，aggregatable 应为 false")
	}
	if len(f.Types) != 1 || f.Types[0] != "text" {
		t.Errorf("Types 应合并为单个 text，得到 %v", f.Types)
	}
}

// TestMergeFields_TypeConflict 同名多类型（text + keyword）：保留全部类型并标 type_conflict。
func TestMergeFields_TypeConflict(t *testing.T) {
	members := []map[string]map[string]capInfo{
		{"env": {"text": cap("text", true, false), "keyword": cap("keyword", true, true)}},
		{"env": {"text": cap("text", true, false), "keyword": cap("keyword", true, true)}},
	}
	fields, _ := mergeFields(members)
	if len(fields) != 1 {
		t.Fatalf("期望 1 个字段")
	}
	f := fields[0]
	if !f.TypeConflict {
		t.Errorf("多类型应标 type_conflict")
	}
	if len(f.Types) != 2 {
		t.Errorf("应保留全部类型，得到 %v", f.Types)
	}
}

// TestMergeFields_Partial 仅部分成员存在：保留并标 partial。
func TestMergeFields_Partial(t *testing.T) {
	members := []map[string]map[string]capInfo{
		{"a": {"text": cap("text", true, false)}},
		{"a": {"text": cap("text", true, false)}, "b": {"long": cap("long", true, true)}},
	}
	fields, _ := mergeFields(members)
	foundA, foundB := false, false
	for i := range fields {
		switch fields[i].Path {
		case "a":
			foundA = true
			if fields[i].Partial {
				t.Errorf("a 两个成员都有，不应 partial")
			}
		case "b":
			foundB = true
			if !fields[i].Partial {
				t.Errorf("b 仅一个成员有，应标 partial")
			}
		}
	}
	if !foundA || !foundB {
		t.Errorf("应同时包含 a 与 b，实际 a=%v b=%v", foundA, foundB)
	}
}

// TestMergeFields_TruncatedStable 字段总数超上限截断：按字段名排序稳定。
func TestMergeFields_TruncatedStable(t *testing.T) {
	// 6 个字段：f0..f5，limit=5 期望截断为按名排序前 5 个（f0..f4）。
	member := map[string]map[string]capInfo{}
	for i := 0; i < 6; i++ {
		name := "f" + string(rune('0'+i))
		member[name] = map[string]capInfo{"keyword": cap("keyword", true, true)}
	}
	fields, truncated := mergeFieldsN([]map[string]map[string]capInfo{member}, 5)
	if !truncated {
		t.Errorf("应标记 truncated")
	}
	if len(fields) != 5 {
		t.Fatalf("应截断为 5 个字段，得到 %d", len(fields))
	}
	for i, f := range fields {
		want := "f" + string(rune('0'+i))
		if f.Path != want {
			t.Fatalf("截断后顺序应稳定为名序，第 %d 个应为 %s，得到 %s", i, want, f.Path)
		}
	}
}

// TestDropMetaFields 元字段（`_` 前缀）应被剔除：它们不在 _source，无法展示与检索。
func TestDropMetaFields(t *testing.T) {
	got := dropMetaFields(map[string]map[string]capInfo{
		"@timestamp": {"date": cap("date", true, true)},
		"_index":     {"_index": cap("_index", true, false)},
		"_seq_no":    {"_seq_no": cap("_seq_no", true, false)},
		"content":    {"text": cap("text", true, false)},
	})
	if len(got) != 2 || got["_index"] != nil || got["_seq_no"] != nil {
		t.Fatalf("期望剔除元字段后剩 @timestamp/content，得到 %#v", got)
	}
}

// TestPickTimeFields 时间字段候选排序：@timestamp 置顶，其次命名字段。
func TestPickTimeFields(t *testing.T) {
	fields := map[string]map[string]capInfo{
		"@timestamp": {"date": cap("date", true, true)},
		"created_at": {"date": cap("date", true, true)},
		"host":       {"keyword": cap("keyword", true, true)},
		"event_time": {"date": cap("date", true, true)},
	}
	got := pickTimeFields(fields)
	if len(got) != 3 {
		t.Fatalf("应选出 3 个 date 字段，得到 %v", got)
	}
	if got[0] != "@timestamp" {
		t.Errorf("@timestamp 应置顶，得到 %s", got[0])
	}
	hasHost := false
	for _, n := range got {
		if n == "host" {
			hasHost = true
		}
	}
	if hasHost {
		t.Errorf("host 非 date 类型，不应入选")
	}
}
