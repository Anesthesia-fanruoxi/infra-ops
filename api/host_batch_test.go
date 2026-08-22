package api

import (
	"fmt"
	"reflect"
	"testing"
)

// ipSeq 生成 172.16.1.start → end 的 IP 序列。
func ipSeq(start, end int) []string {
	var out []string
	for i := start; i <= end; i++ {
		out = append(out, fmt.Sprintf("172.16.1.%d", i))
	}
	return out
}

func TestParseIPList(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{"单IP", "172.16.2.5", []string{"172.16.2.5"}, false},
		{"简写范围", "172.16.1.11-20", ipSeq(11, 20), false},
		{"完整范围", "172.16.1.11-172.16.1.13", ipSeq(11, 13), false},
		{"混合输入", "172.16.1.11-12\n172.16.2.5", append(ipSeq(11, 12), "172.16.2.5"), false},
		{"四种分隔符", "1.1.1.1,2.2.2.2;3.3.3.3 4.4.4.4", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}, false},
		{"重复去重", "172.16.1.5\n172.16.1.5", []string{"172.16.1.5"}, false},
		{"范围与单IP重复", "172.16.1.11-12\n172.16.1.12", ipSeq(11, 12), false},
		{"空输入", "  \n  , ; ", nil, false},
		{"非法字母", "172.16.1.a", nil, true},
		{"越界段", "172.16.1.256", nil, true},
		{"缺段", "172.16.1", nil, true},
		{"空段", "172.16..5", nil, true},
		{"范围终点非法", "172.16.1.1-abc", nil, true},
		{"范围倒挂", "172.16.1.20-10", nil, true},
		{"范围跨网段", "172.16.1.1-172.16.2.1", nil, true},
		{"超上限", "10.0.0.1-200", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIPList(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误，得到 nil（got=%v）", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestParseIPListMax(t *testing.T) {
	// 恰好 100 台应通过
	raw := "10.0.0.1-100"
	got, err := parseIPList(raw)
	if err != nil {
		t.Fatalf("100 台不应超限: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("got %d want 100", len(got))
	}

	// 101 台应报 errTooManyIPs
	if _, err := parseIPList("10.0.0.1-101"); err == nil {
		t.Fatal("101 台应超限")
	}
}

func TestValidIPv4(t *testing.T) {
	valid := []string{"0.0.0.0", "255.255.255.255", "172.16.1.1", "10.0.0.1"}
	for _, s := range valid {
		if !validIPv4(s) {
			t.Errorf("应合法: %s", s)
		}
	}
	invalid := []string{"", "256.1.1.1", "1.2.3", "1.2.3.4.5", "1.2.3.abc", "1.2.3.-1"}
	for _, s := range invalid {
		if validIPv4(s) {
			t.Errorf("应非法: %s", s)
		}
	}
}

func TestUniqueName(t *testing.T) {
	// 库内已有 k8s-node、k8s-node-2
	used := map[string]bool{"k8s-node": true, "k8s-node-2": true}

	// 不冲突：直接用原名
	if got := uniqueName("web-01", used); got != "web-01" {
		t.Fatalf("不冲突应保持原名, got=%s", got)
	}
	// 冲突：跳到 -2
	if got := uniqueName("k8s-node", used); got != "k8s-node-3" {
		t.Fatalf("冲突应追加 -3（-2 也已被占）, got=%s", got)
	}
	// 本批内已分配也要避开
	if got := uniqueName("web-01", used); got != "web-01-2" {
		t.Fatalf("批内冲突应追加 -2, got=%s", got)
	}
	// 空 hostname 回退场景（IP 作为 base，通常不冲突）
	if got := uniqueName("172.16.1.11", used); got != "172.16.1.11" {
		t.Fatalf("IP 作名称应保持, got=%s", got)
	}
}
