package redis

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// scale_test.go：缩容保护（含哨兵多数派）与缩容软下线参数的用例。

// 哨兵仲裁保护：缩容后哨兵个数（=每台主机各一个）少于多数派时拒绝；其余模式不受影响。
func TestScaleGuardSentinelMajority(t *testing.T) {
	d := New()
	hosts := func(n int) []model.StackInstanceHost {
		out := make([]model.StackInstanceHost, 0, n)
		for i := 1; i <= n; i++ {
			role := "worker"
			if i == 1 {
				role = "master"
			}
			out = append(out, model.StackInstanceHost{HostID: int64(i), Role: role})
		}
		return out
	}
	active := hosts(4)

	// 4 → 2：哨兵只剩 2 个，quorum（len(hosts)/2+1）无法达成 → 拒绝
	err := d.ScaleGuard(stackkit.ScaleCtx{Mode: "sentinel", Active: active, RemoveIDs: []int64{3, 4}, Remain: 2})
	if err == nil || !strings.Contains(err.Error(), "多数派") {
		t.Fatalf("期望哨兵多数派拦截，实得 %v", err)
	}
	// 4 → 3：仍构成多数派 → 放行
	if err := d.ScaleGuard(stackkit.ScaleCtx{Mode: "sentinel", Active: active, RemoveIDs: []int64{4}, Remain: 3}); err != nil {
		t.Fatalf("3 台哨兵应放行: %v", err)
	}
	// 原有保护不回归：移除唯一主节点仍须拦截
	if err := d.ScaleGuard(stackkit.ScaleCtx{Mode: "sentinel", Active: active, RemoveIDs: []int64{1}, Remain: 3}); err == nil {
		t.Fatal("期望拦截移除唯一主节点")
	}
	// 主从模式不套用哨兵下限（2 → 1）
	if err := d.ScaleGuard(stackkit.ScaleCtx{Mode: "replication", Active: active[:2], RemoveIDs: []int64{2}, Remain: 1}); err != nil {
		t.Fatalf("主从模式不应触发哨兵校验: %v", err)
	}
	// 集群下限保持原样
	if err := d.ScaleGuard(stackkit.ScaleCtx{Mode: "cluster", Remain: 2}); err == nil {
		t.Fatal("集群少于 3 节点应拦截")
	}
}

// 缩容软下线：仅集群模式声明，参数为待移除节点的 ip:port（逐主机端口）。
func TestDrainParams(t *testing.T) {
	d := New()
	if _, need := d.DrainParams(stackkit.DrainCtx{Mode: "replication", Removed: []model.StackRunHost{{HostIP: "10.0.0.1"}}}); need {
		t.Fatal("主从模式不需要软下线")
	}
	if _, need := d.DrainParams(stackkit.DrainCtx{Mode: "sentinel", Removed: []model.StackRunHost{{HostIP: "10.0.0.1"}}}); need {
		t.Fatal("哨兵模式不需要软下线")
	}
	got, need := d.DrainParams(stackkit.DrainCtx{Mode: "cluster", Removed: []model.StackRunHost{
		{HostIP: "10.0.0.2", ParamsJSON: `{"port":"6380"}`},
		{HostIP: "10.0.0.1"},
	}})
	if !need {
		t.Fatal("集群模式应声明软下线")
	}
	if got["remove_nodes"] != "10.0.0.1:6379,10.0.0.2:6380" {
		t.Fatalf("remove_nodes=%q", got["remove_nodes"])
	}
}
