package nacos

import (
	"strconv"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

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
