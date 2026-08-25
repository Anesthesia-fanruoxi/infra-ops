package store

import (
	"testing"

	"infra-ops/model"
)

func mk(id int64, name, ip string) model.Host {
	return model.Host{ID: id, Name: name, IP: ip}
}

func ids(hs []model.Host) []int64 {
	out := make([]int64, len(hs))
	for i := range hs {
		out[i] = hs[i].ID
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortHostsByNameAsc(t *testing.T) {
	hs := []model.Host{mk(1, "web2", "2.2.2.2"), mk(2, "Web10", "1.1.1.1"), mk(3, "db", "3.3.3.3")}
	SortHosts(hs, "name", "asc")
	// 忽略大小写：db < Web10 < web2
	if got := ids(hs); !eq(got, []int64{3, 2, 1}) {
		t.Fatalf("name asc = %v", got)
	}
}

func TestSortHostsByIPNumeric(t *testing.T) {
	hs := []model.Host{mk(1, "a", "10.0.0.2"), mk(2, "b", "2.0.0.1"), mk(3, "c", "192.168.1.5"), mk(4, "d", "bad-ip")}
	SortHosts(hs, "ip", "asc")
	// 数值序：2.0.0.1 < 10.0.0.2 < 192.168.1.5，非法 IP 最后
	if got := ids(hs); !eq(got, []int64{2, 1, 3, 4}) {
		t.Fatalf("ip asc = %v", got)
	}
	SortHosts(hs, "ip", "desc")
	if got := ids(hs); !eq(got, []int64{4, 3, 1, 2}) {
		t.Fatalf("ip desc = %v", got)
	}
}

func TestSortHostsDefaultIsName(t *testing.T) {
	hs := []model.Host{mk(2, "b", "9.9.9.9"), mk(1, "a", "8.8.8.8")}
	SortHosts(hs, "", "") // 空参数回退默认主机名升序
	if got := ids(hs); !eq(got, []int64{1, 2}) {
		t.Fatalf("default = %v", got)
	}
}
