package es

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestSyncLockMutualExclusion 单视图互斥：锁被占用时，并发尝试无人能获得。
func TestSyncLockMutualExclusion(t *testing.T) {
	l := &syncLock{}
	viewID := int64(7)

	// 先占住锁，制造「正在同步中」的确定状态
	if !l.tryAcquire(viewID) {
		t.Fatal("初始占用应成功")
	}

	var obtained int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.tryAcquire(viewID) {
				atomic.AddInt32(&obtained, 1)
			}
		}()
	}
	wg.Wait()

	if obtained != 0 {
		t.Fatalf("锁被占用时并发尝试应全部失败，实际 %d 次成功", obtained)
	}
	l.release(viewID)
}

// TestSyncLockSerialReacquire 释放后他人可重新获得（可复用）。
func TestSyncLockSerialReacquire(t *testing.T) {
	l := &syncLock{}
	viewID := int64(9)
	if !l.tryAcquire(viewID) {
		t.Fatal("第一次获取应成功")
	}
	if l.tryAcquire(viewID) {
		t.Fatal("未释放时重复获取应失败")
	}
	l.release(viewID)
	if !l.tryAcquire(viewID) {
		t.Fatal("释放后应可重新获取")
	}
	l.release(viewID)
}

// TestSyncLockDistinctViews 不同视图互不阻塞。
func TestSyncLockDistinctViews(t *testing.T) {
	l := &syncLock{}
	lock1 := l.tryAcquire(1)
	lock2 := l.tryAcquire(2)
	if !lock1 || !lock2 {
		t.Fatal("不同视图应同时可占用")
	}
	l.release(1)
	l.release(2)
}
