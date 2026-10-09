package main

import (
	"os"
	"path/filepath"
	"testing"
)

// stagePend 在临时目录里摆出一份完整的恢复暂存。
func stagePend(t *testing.T, dbContent string, withAssets bool) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("data", "restore_pending", "assets"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "restore_pending", pendDB), []byte(dbContent), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "restore_pending", pendMark), []byte(`{"created_at":"test"}`), 0640); err != nil {
		t.Fatal(err)
	}
	if withAssets {
		if err := os.WriteFile(filepath.Join("data", "restore_pending", "assets", "x.jar"), []byte("asset-bytes"), 0640); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplyRestorePendingNoop(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := applyRestorePending(); err != nil {
		t.Fatalf("无暂存应为 no-op: %v", err)
	}
}

func TestApplyRestorePendingSwaps(t *testing.T) {
	stagePend(t, "new-db-content", true)
	// 造旧现场：旧库 + 旧资产
	os.MkdirAll("data/assets", 0750)
	os.WriteFile(filepath.Join("data", "infra-ops.db"), []byte("old-db"), 0600)
	os.WriteFile(filepath.Join("data", "infra-ops.db-wal"), []byte("old-wal"), 0600)
	os.WriteFile(filepath.Join("data", "assets", "old.jar"), []byte("old-asset"), 0640)

	if err := applyRestorePending(); err != nil {
		t.Fatalf("换入失败: %v", err)
	}
	// 新库就位、暂存目录消失
	if b, _ := os.ReadFile(filepath.Join("data", "infra-ops.db")); string(b) != "new-db-content" {
		t.Fatalf("库未被换入: %q", string(b))
	}
	if _, err := os.Stat(pendDir); !os.IsNotExist(err) {
		t.Fatal("暂存目录应被清理")
	}
	// 旧库与旧资产进备份目录
	ents, _ := filepath.Glob("data/pre-restore-*/infra-ops.db")
	if len(ents) != 1 {
		t.Fatalf("旧库未进备份目录: %v", ents)
	}
	if b, _ := os.ReadFile(ents[0]); string(b) != "old-db" {
		t.Fatalf("备份的旧库内容不符: %q", string(b))
	}
	if b, _ := os.ReadFile(filepath.Join("data", "assets", "x.jar")); string(b) != "asset-bytes" {
		t.Fatal("资产未被换入")
	}
	oldJar, _ := filepath.Glob("data/pre-restore-*/assets/old.jar")
	if len(oldJar) != 1 {
		t.Fatalf("旧资产未进备份目录: %v", oldJar)
	}
}

func TestApplyRestorePendingBrokenStage(t *testing.T) {
	stagePend(t, "half-written", false)
	os.Remove(filepath.Join("data", "restore_pending", pendDB)) // 有标志没库 = 坏暂存
	// 旧库保持原样
	os.MkdirAll("data", 0750)
	os.WriteFile(filepath.Join("data", "infra-ops.db"), []byte("old-db"), 0600)

	if err := applyRestorePending(); err != nil {
		t.Fatalf("坏暂存应被挪走而非报错: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join("data", "infra-ops.db")); string(b) != "old-db" {
		t.Fatal("坏暂存不得动旧库")
	}
	ents, _ := filepath.Glob("data/restore_broken-*")
	if len(ents) != 1 {
		t.Fatalf("坏暂存未挪到待查位置: %v", ents)
	}
}

// 说明：换入中途失败的回滚分支未做自动化测试——Windows 的
// MoveFileEx(REPLACE_EXISTING) 连「目录替换文件」都会成功，常规手段构不成
// 必败现场（锁目标文件要 syscall 级共享位控制，得不偿失）。回滚只是把
// 备份目录里的旧文件原样 rename 回来，与 moveAll 同一原语，靠 Swaps 用例
// 与代码评审覆盖；真机恢复前建议先手动跑一次导出确认可回退。
