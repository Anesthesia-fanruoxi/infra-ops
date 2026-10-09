package mysql

import (
	"context"
	"testing"
)

// TestQueryCancelRegistry 「取消」登记表的语义：
// 登记后可取消；收尾后不再命中；同连接被并发覆盖时旧凭据收尾不误删新登记。
func TestQueryCancelRegistry(t *testing.T) {
	r := newQueryCancelRegistry()

	// 登记 → 取消：ctx 当场被取消
	ctx1, cancel1 := context.WithCancel(context.Background())
	t1 := r.register(1, cancel1)
	if !r.cancel(1) {
		t.Fatal("登记后应可取消")
	}
	select {
	case <-ctx1.Done():
	default:
		t.Fatal("cancel 后 ctx 应已取消")
	}

	// 收尾后：没有在执行，取消不命中；重复收尾幂等
	r.finish(1, t1)
	if r.cancel(1) {
		t.Fatal("收尾后不应再命中")
	}
	r.finish(1, t1)

	// 覆盖场景：同连接先后两条，旧凭据收尾不能误删新登记
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	t2 := r.register(1, cancel2)
	r.register(1, cancel3)
	r.finish(1, t2)
	if !r.cancel(1) {
		t.Fatal("新登记被旧凭据误删")
	}
	select {
	case <-ctx3.Done():
	default:
		t.Fatal("取消应落在新登记上")
	}
	select {
	case <-ctx2.Done():
		t.Fatal("旧登记不应被取消")
	default:
	}
}
