package rabbitmq

import (
	"strconv"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// scale_test.go：Q1（ScaleGuard 下限 3 台）与 Q2（DrainParams 软下线参数 + scale_in 阶段）测试。

// scaleCtx 造 active 台在役成员、末 remove 台待移除的缩容上下文。
func scaleCtx(mode string, active, remove int) stackkit.ScaleCtx {
	hosts := make([]model.StackInstanceHost, 0, active)
	for i := 1; i <= active; i++ {
		hosts = append(hosts, model.StackInstanceHost{HostID: int64(i), HostIP: "10.0.0." + strconv.Itoa(i), Role: "worker"})
	}
	ids := make([]int64, 0, remove)
	for i := active - remove + 1; i <= active; i++ {
		ids = append(ids, int64(i))
	}
	return stackkit.ScaleCtx{Mode: mode, Active: hosts, RemoveIDs: ids, Remain: active - remove}
}

// 3→2：低于 3 台，必须拦截（文案含「至少保留 3 台」）。
func TestScaleGuardBlocksBelowThree(t *testing.T) {
	err := New().ScaleGuard(scaleCtx("cluster", 3, 1))
	if err == nil {
		t.Fatal("3→2 应被拦截")
	}
	if !strings.Contains(err.Error(), "至少保留 3 台") {
		t.Fatalf("文案: %v", err)
	}
}

// 4→3：仍不低于 3 台，放行。
func TestScaleGuardAllowsThree(t *testing.T) {
	if err := New().ScaleGuard(scaleCtx("cluster", 4, 1)); err != nil {
		t.Fatalf("4→3 应放行: %v", err)
	}
}

// 未知模式 / 无在役成员：不拦截（与通用层兜底一致）。
func TestScaleGuardEdges(t *testing.T) {
	if err := New().ScaleGuard(scaleCtx("nope", 2, 1)); err != nil {
		t.Fatalf("未知模式不应拦截: %v", err)
	}
	if err := New().ScaleGuard(stackkit.ScaleCtx{Mode: "cluster"}); err != nil {
		t.Fatalf("无在役成员不应拦截: %v", err)
	}
}

// DrainParams：cluster 缩容给出 rabbit@<ip> 节点名列表（排序稳定），供 scale-in.sh 逐个软下线。
func TestDrainParams(t *testing.T) {
	ctx := stackkit.DrainCtx{
		Mode: "cluster",
		Removed: []model.StackRunHost{
			{HostID: 3, HostIP: "10.0.0.3"},
			{HostID: 2, HostIP: "10.0.0.2"},
		},
	}
	params, ok := New().DrainParams(ctx)
	if !ok {
		t.Fatal("cluster 缩容应声明软下线")
	}
	if got, want := params["remove_nodes"], "rabbit@10.0.0.2,rabbit@10.0.0.3"; got != want {
		t.Fatalf("remove_nodes = %q, want %q", got, want)
	}
}

// 未知模式 / 无移除主机：不声明软下线（引擎降级为直接停服）。
func TestDrainParamsEdges(t *testing.T) {
	if _, ok := New().DrainParams(stackkit.DrainCtx{Mode: "nope"}); ok {
		t.Fatal("未知模式不应声明软下线")
	}
	if _, ok := New().DrainParams(stackkit.DrainCtx{Mode: "cluster"}); ok {
		t.Fatal("无移除主机不应声明软下线")
	}
}

// scale_in 阶段映射到软下线脚本（软下线链路的脚本装载入口）。
func TestPhaseScaleIn(t *testing.T) {
	pf, ok := New().Phase("cluster", stackkit.PhaseScaleIn)
	if !ok || pf.Path != "stacks/rabbitmq/scripts/scale-in.sh" {
		t.Fatalf("Phase = %+v, ok=%v", pf, ok)
	}
	if _, ok := New().Phase("nope", stackkit.PhaseScaleIn); ok {
		t.Fatal("未知模式不应有 scale_in 阶段")
	}
}
