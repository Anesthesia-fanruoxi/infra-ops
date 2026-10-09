// restore.go 启动时换入恢复暂存（package main）。
//
// 恢复接口（api/settings/backup.go）只把备份内容落进 data/restore_pending/，
// 真正替换发生在下次启动、store.Open 之前——进程内 SQLite 持有句柄时热替换
// 是未定义行为。换入前旧库与旧资产整体挪进 data/pre-restore-<时间戳>/，
// 留作手动回滚；换入中途任何失败都把旧文件原样挪回来，绝不留半套数据。
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

// pendDirs 恢复暂存目录的固定布局（与 api/settings/backup.go 约定一致）。
const (
	pendDir   = "data/restore_pending"
	pendDB    = "infra-ops.db"
	pendMark  = "manifest.json" // 暂存完成标志：有它才认定这是一次完整暂存
	pendAsset = "assets"
)

// applyRestorePending 检查并执行恢复暂存的换入；无暂存时是 no-op。
// 返回 error 只表示换入失败已回滚旧数据——旧库完好，程序照常启动。
func applyRestorePending() error {
	mark := filepath.Join(pendDir, pendMark)
	if _, err := os.Stat(mark); err != nil {
		return nil // 无暂存（常态）
	}
	if _, err := os.Stat(filepath.Join(pendDir, pendDB)); err != nil {
		// 有标志没库文件 = 上次暂存写坏了一半，换入无从谈起：留着现场报警告
		_, err := renameAside(pendDir, "restore_broken")
		return err
	}

	backupDir, err := renameAside("", "pre-restore")
	if err == nil {
		// 旧库（含 WAL 残页）与旧资产挪进备份目录；不存在就跳过（全新环境也可能恢复）
		err = moveAll(map[string]string{
			dbPath:          filepath.Join(backupDir, "infra-ops.db"),
			dbPath + "-wal": filepath.Join(backupDir, "infra-ops.db-wal"),
			dbPath + "-shm": filepath.Join(backupDir, "infra-ops.db-shm"),
			"data/assets":   filepath.Join(backupDir, pendAsset),
		})
	}
	if err == nil {
		// 暂存内容换入正式位置
		err = moveAll(map[string]string{
			filepath.Join(pendDir, pendDB):    dbPath,
			filepath.Join(pendDir, pendAsset): "data/assets",
		})
	}
	if err != nil {
		// 换入失败：旧文件从备份目录挪回原位，暂存目录让位改名待查
		if backupDir != "" {
			rollback(backupDir)
		}
		renameAside(pendDir, "restore_failed")
		return err
	}

	// 收尾：暂存目录清掉，日志说明换入结果（排障时 app.log 第一行就能看到）
	meta, _ := os.ReadFile(mark)
	log.Printf("恢复暂存已换入，备份时点信息：%s；旧数据保留在 %s", string(meta), backupDir)
	return os.RemoveAll(pendDir)
}

// renameAside 把 dir（空串 = 新建目录）改名成 <name>-<时间戳> 形式，返回新路径。
func renameAside(dir, name string) (string, error) {
	dst := "data/" + name + "-" + time.Now().Format("20060102-150405")
	var err error
	if dir == "" {
		err = os.MkdirAll(dst, 0750)
	} else {
		err = os.Rename(dir, dst)
	}
	if err != nil {
		return "", err
	}
	return dst, nil
}

// moveAll 逐项 rename；src 不存在视为无事发生，其余失败立即返回。
func moveAll(pairs map[string]string) error {
	for src, dst := range pairs {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// rollback 换入失败时把备份目录里的旧文件挪回原位（尽力而为，失败只记日志）。
func rollback(backupDir string) {
	pairs := map[string]string{
		filepath.Join(backupDir, pendDB):    dbPath,
		filepath.Join(backupDir, pendAsset): "data/assets",
	}
	for src, dst := range pairs {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			log.Printf("回滚 %s 失败：%v（旧数据仍在 %s）", src, err, backupDir)
		}
	}
}
