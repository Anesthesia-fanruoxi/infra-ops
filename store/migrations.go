package store

import "database/sql"

type migration struct {
	version int
	fn      func(db *sql.DB) error
}

var migrations = []migration{
	{1, migrateV1},
	{2, migrateV2},
	{3, migrateV3},
	{4, migrateV4},
	{5, migrateV5},
	{6, migrateV6},
	{7, migrateV7},
}

// migrateV5 创建 settings 表：全部运行配置以 KV 形式持久化于此。
func migrateV5(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
	)`)
	return err
}

// migrateV6 创建部署中心三张表并预置内置模板。
func migrateV6(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS deploy_templates (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			script      TEXT NOT NULL,
			variables   TEXT NOT NULL DEFAULT '[]',
			is_builtin  INTEGER NOT NULL DEFAULT 0,
			created_at  TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at  TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_tasks (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			template_id   INTEGER NOT NULL REFERENCES deploy_templates(id),
			template_name TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','success','partial','failed')),
			total         INTEGER NOT NULL DEFAULT 0,
			success_cnt   INTEGER NOT NULL DEFAULT 0,
			fail_cnt      INTEGER NOT NULL DEFAULT 0,
			created_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			finished_at   TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_tasks_created ON deploy_tasks(created_at)`,
		`CREATE TABLE IF NOT EXISTS deploy_task_hosts (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id     INTEGER NOT NULL REFERENCES deploy_tasks(id) ON DELETE CASCADE,
			host_id     INTEGER NOT NULL,
			host_name   TEXT NOT NULL DEFAULT '',
			host_ip     TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','success','failed')),
			output      TEXT NOT NULL DEFAULT '',
			error       TEXT NOT NULL DEFAULT '',
			started_at  TEXT,
			finished_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dth_task ON deploy_task_hosts(task_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV7 创建定时任务表并为部署任务补充来源字段。
func migrateV7(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS deploy_schedules (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT NOT NULL UNIQUE,
			template_id  INTEGER NOT NULL REFERENCES deploy_templates(id),
			host_ids     TEXT NOT NULL DEFAULT '[]',
			params_json  TEXT NOT NULL DEFAULT '{}',
			cron_expr    TEXT NOT NULL,
			enabled      INTEGER NOT NULL DEFAULT 1,
			last_task_id INTEGER,
			last_run_at  TEXT,
			next_run_at  TEXT,
			created_at   TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`ALTER TABLE deploy_tasks ADD COLUMN schedule_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE deploy_tasks ADD COLUMN trigger_type TEXT NOT NULL DEFAULT 'manual'`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func migrateV1(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS credentials (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			type             TEXT NOT NULL CHECK (type IN ('private_key','password')),
			username         TEXT NOT NULL DEFAULT 'root',
			encrypted_secret BLOB NOT NULL,
			fingerprint      TEXT NOT NULL DEFAULT '',
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE TABLE IF NOT EXISTS hosts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL UNIQUE,
			ip            TEXT NOT NULL,
			port          INTEGER NOT NULL DEFAULT 22,
			tag           TEXT NOT NULL DEFAULT 'other',
			remark        TEXT NOT NULL DEFAULT '',
			credential_id INTEGER NOT NULL REFERENCES credentials(id),
			status        TEXT NOT NULL DEFAULT 'unverified' CHECK (status IN ('online','offline','unverified')),
			latency_ms    INTEGER NOT NULL DEFAULT 0,
			info_json     TEXT NOT NULL DEFAULT '{}',
			last_check_at TEXT,
			created_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at    TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hosts_tag    ON hosts(tag)`,
		`CREATE INDEX IF NOT EXISTS idx_hosts_status ON hosts(status)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			action      TEXT NOT NULL,
			target_type TEXT NOT NULL DEFAULT '',
			target_id   INTEGER NOT NULL DEFAULT 0,
			detail      TEXT NOT NULL DEFAULT '',
			remote_ip   TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_action  ON audit_logs(action)`,
		`CREATE TABLE IF NOT EXISTS host_keys (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			addr        TEXT NOT NULL UNIQUE,
			fingerprint TEXT NOT NULL,
			first_seen  TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			last_seen   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func migrateV2(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE hosts ADD COLUMN tags TEXT NOT NULL DEFAULT '[]'`)
	return err
}

func migrateV3(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS host_tag_dict`)
	return err
}

func migrateV4(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(hosts)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasTag, hasRole := false, false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		switch name {
		case "tag":
			hasTag = true
		case "role":
			hasRole = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if !hasTag {
		if _, err = tx.Exec(`ALTER TABLE hosts ADD COLUMN tag TEXT NOT NULL DEFAULT 'other'`); err != nil {
			return err
		}
	}
	if hasRole {
		if _, err = tx.Exec(`UPDATE hosts SET tag=CASE WHEN trim(tag)='' OR tag='other' THEN COALESCE(NULLIF(trim(role),''),'other') ELSE tag END`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_hosts_tag ON hosts(tag)`); err != nil {
		return err
	}
	return tx.Commit()
}
