// Package store 负责 SQLite 数据存取，不含业务逻辑。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB 全局数据库连接。
var DB *sql.DB

// Open 打开 SQLite 连接并设置 WAL 模式。
func Open(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}

	if err := conn.Ping(); err != nil {
		conn.Close()
		return fmt.Errorf("ping sqlite: %w", err)
	}

	DB = conn
	return nil
}

// Close 关闭数据库连接。
func Close() {
	if DB != nil {
		DB.Close()
	}
}

// Migrate 执行所有未应用的迁移。
func Migrate() error {
	if err := ensureMigrationsTable(); err != nil {
		return err
	}
	for _, m := range migrations {
		applied, err := isMigrationApplied(m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := m.fn(DB); err != nil {
			return fmt.Errorf("migration v%d: %w", m.version, err)
		}
		if err := recordMigration(m.version); err != nil {
			return err
		}
	}
	// 内置模板随版本演进，每次启动幂等刷新（缺失则插入，脚本有变更则更新）
	return seedBuiltinTemplates(DB)
}

func ensureMigrationsTable() error {
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
	)`)
	return err
}

func isMigrationApplied(version int) (bool, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", version).Scan(&count)
	return count > 0, err
}

func recordMigration(version int) error {
	_, err := DB.Exec("INSERT INTO schema_migrations(version) VALUES(?)", version)
	return err
}
