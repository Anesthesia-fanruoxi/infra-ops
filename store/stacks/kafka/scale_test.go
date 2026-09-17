package kafka

import (
	"strconv"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// scaleCtx 造一个 n 台在役、移除 removeIDs 的缩容上下文。
func scaleCtx(mode string, n int, remove ...int64) stackkit.ScaleCtx {
	active := make([]model.StackInstanceHost, 0, n)
	for i := 1; i <= n; i++ {
		active = append(active, model.StackInstanceHost{
			ID: int64(i), HostID: int64(i), HostIP: "10.0.0." + strconv.Itoa(i),
		})
	}
	return stackkit.ScaleCtx{Mode: mode, Active: active, RemoveIDs: remove, Remain: n - len(remove)}
}

// 3 台缩到 2 台：仍满足多数派（2 ≥ ⌊3/2⌋+1），但触到生产下限 3 → 拦。
func TestScaleGuardRejectsBelowThree(t *testing.T) {
	d := New()
	err := d.ScaleGuard(scaleCtx("kraft", 3, 3))
	if err == nil {
		t.Fatal("3→2 应被拦截")
	}
	if !strings.Contains(err.Error(), "至少保留 3 台") {
		t.Fatalf("文案应说明下限: %v", err)
	}
	// 5→4 同样落到 4 台：满足多数派（4 ≥ 3）且 ≥3 → 放行
	if err := d.ScaleGuard(scaleCtx("kraft", 5, 5)); err != nil {
		t.Fatalf("5→4 应放行: %v", err)
	}
}

// 3 台缩到 1 台：先撞多数派闸（1 < 2）。
func TestScaleGuardRejectsLostQuorum(t *testing.T) {
	d := New()
	err := d.ScaleGuard(scaleCtx("zk", 3, 2, 3))
	if err == nil {
		t.Fatal("3→1 应被拦截")
	}
	if !strings.Contains(err.Error(), "多数派") {
		t.Fatalf("文案应点明多数派: %v", err)
	}
	// 模式名要落到文案里，便于定位是哪个仲裁
	if !strings.Contains(err.Error(), "ZooKeeper") {
		t.Fatalf("文案应含模式名: %v", err)
	}
}

// 扩容后的集群：多数派按当前在役数算（保守），移除新加入的 broker-only 成员应放行。
func TestScaleGuardAfterScaleOut(t *testing.T) {
	d := New()
	// 存量 3 + 扩容 1 = 4 台，移除其中 1 台 → 剩 3 台
	if err := d.ScaleGuard(scaleCtx("kraft", 4, 4)); err != nil {
		t.Fatalf("4→3 应放行: %v", err)
	}
	// 同样 4 台里移除 2 台 → 剩 2 台，低于 3 → 拦
	if err := d.ScaleGuard(scaleCtx("kraft", 4, 3, 4)); err == nil {
		t.Fatal("4→2 应被拦截")
	}
}

// 非 kafka 模式（防御性）：不认识即放行，交由引擎通用校验。
func TestScaleGuardUnknownMode(t *testing.T) {
	d := New()
	if err := d.ScaleGuard(scaleCtx("unknown", 3, 2, 3)); err != nil {
		t.Fatalf("未知模式不应拦: %v", err)
	}
	// 空成员（实例已无在役主机）不做判定，交给引擎的通用下限
	if err := d.ScaleGuard(stackkit.ScaleCtx{Mode: "kraft", Remain: 0}); err != nil {
		t.Fatalf("空在役列表不应拦: %v", err)
	}
}
