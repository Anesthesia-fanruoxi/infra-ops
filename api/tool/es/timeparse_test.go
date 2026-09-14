package es

import "testing"

func TestParseTimeRangeOK(t *testing.T) {
	start, end, err := ParseTimeRange("2026-09-11 17:00:00", "2026-09-11 18:00:00")
	if err != nil {
		t.Fatalf("解析合法区间应成功，得到 %v", err)
	}
	if start != 1789117200000 {
		t.Errorf("起始毫秒错误：start=%d, want 1789117200000", start)
	}
	if end-start != 3600*1000 {
		t.Errorf("结束与起始应差 1 小时，得到 %d", end-start)
	}
}

func TestParseTimeRangeRejects(t *testing.T) {
	cases := []struct {
		name  string
		start string
		end   string
	}{
		{"缺失 start", "", "2026-09-11 18:00:00"},
		{"缺失 end", "2026-09-11 17:00:00", ""},
		{"空串", "  ", "2026-09-11 18:00:00"},
		{"缺秒", "2026-09-11 17:00", "2026-09-11 18:00:00"},
		{"T 分隔", "2026-09-11T17:00:00", "2026-09-11 18:00:00"},
		{"相对表达式", "now-1h", "2026-09-11 18:00:00"},
		{"毫秒数字", "1789117200000", "1789120800000"},
		{"start 格式坏", "2026/09/11 17:00:00", "2026-09-11 18:00:00"},
		{"end 非法月", "2026-09-11 17:00:00", "2026-13-11 18:00:00"},
		{"start>=end", "2026-09-11 18:00:00", "2026-09-11 18:00:00"},
		{"start>end", "2026-09-11 19:00:00", "2026-09-11 18:00:00"},
	}
	for _, c := range cases {
		if _, _, err := ParseTimeRange(c.start, c.end); err == nil {
			t.Errorf("[%s] 应拒绝并返回错误", c.name)
		}
	}
}

func TestParseDateTimeUTC8(t *testing.T) {
	ms, err := parseDateTime("2026-09-11 17:00:00")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if ms != 1789117200000 {
		t.Errorf("UTC+8 换算错误：got %d, want 1789117200000", ms)
	}
}
