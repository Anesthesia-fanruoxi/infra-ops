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
	{8, migrateV8},
	{9, migrateV9},
	{10, migrateV10},
	{11, migrateV11},
	{12, migrateV12},
}

// migrateV8 并发配置改为自适应：存量库中未改过的旧引导默认值 5 归一为 auto。
func migrateV8(db *sql.DB) error {
	_, err := db.Exec(`UPDATE settings SET value='auto' WHERE key='probe.concurrency' AND value='5'`)
	return err
}

// migrateV9 主机安装标记表：记录每台主机执行过哪些安装模板；主机删除时级联清理。
func migrateV9(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS host_installs (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		host_id       INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
		template_id   INTEGER NOT NULL DEFAULT 0,
		template_name TEXT NOT NULL,
		task_id       INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
		updated_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
		UNIQUE(host_id, template_name)
	)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_host_installs_host ON host_installs(host_id)`)
	return err
}

// migrateV12 任务编排：定义/步骤/运行/运行明细（by_host 差异化链表一并建好，P2 启用）。
func migrateV12(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS orchestrations (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT NOT NULL UNIQUE,
			description  TEXT NOT NULL DEFAULT '',
			exec_mode    TEXT NOT NULL DEFAULT 'by_step' CHECK (exec_mode IN ('by_host','by_step')),
			enabled      INTEGER NOT NULL DEFAULT 1,
			created_at   TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE TABLE IF NOT EXISTS orchestration_steps (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			orchestration_id   INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
			seq                INTEGER NOT NULL,
			template_id        INTEGER NOT NULL REFERENCES deploy_templates(id),
			params_json        TEXT NOT NULL DEFAULT '{}',
			host_scope         TEXT NOT NULL DEFAULT '',
			continue_on_error  INTEGER NOT NULL DEFAULT 0,
			retry_count        INTEGER NOT NULL DEFAULT 0,
			retry_interval_sec INTEGER NOT NULL DEFAULT 30,
			timeout_sec        INTEGER NOT NULL DEFAULT 0,
			UNIQUE(orchestration_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS orchestration_host_chains (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			orchestration_id   INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
			host_id            INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
			seq                INTEGER NOT NULL,
			template_id        INTEGER NOT NULL REFERENCES deploy_templates(id),
			params_json        TEXT NOT NULL DEFAULT '{}',
			continue_on_error  INTEGER NOT NULL DEFAULT 0,
			retry_count        INTEGER NOT NULL DEFAULT 0,
			retry_interval_sec INTEGER NOT NULL DEFAULT 30,
			UNIQUE(orchestration_id, host_id, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ohc_orch ON orchestration_host_chains(orchestration_id, host_id)`,
		`CREATE TABLE IF NOT EXISTS orchestration_step_host_vars (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			orchestration_id INTEGER NOT NULL REFERENCES orchestrations(id) ON DELETE CASCADE,
			seq              INTEGER NOT NULL,
			host_id          INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
			params_json      TEXT NOT NULL DEFAULT '{}',
			UNIQUE(orchestration_id, seq, host_id)
		)`,
		`CREATE TABLE IF NOT EXISTS orchestration_runs (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			orchestration_id INTEGER NOT NULL,
			name             TEXT NOT NULL DEFAULT '',
			exec_mode        TEXT NOT NULL DEFAULT 'by_step',
			status           TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','success','partial','failed')),
			total_hosts      INTEGER NOT NULL DEFAULT 0,
			ok_hosts         INTEGER NOT NULL DEFAULT 0,
			fail_hosts       INTEGER NOT NULL DEFAULT 0,
			trigger_type     TEXT NOT NULL DEFAULT 'manual',
			host_ids         TEXT NOT NULL DEFAULT '[]',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			finished_at      TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_oruns_created ON orchestration_runs(created_at)`,
		`CREATE TABLE IF NOT EXISTS orchestration_run_steps (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id        INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
			host_id       INTEGER NOT NULL,
			host_name     TEXT NOT NULL DEFAULT '',
			host_ip       TEXT NOT NULL DEFAULT '',
			seq           INTEGER NOT NULL,
			template_id   INTEGER NOT NULL DEFAULT 0,
			template_name TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','success','failed','skipped')),
			attempt       INTEGER NOT NULL DEFAULT 1,
			output        TEXT NOT NULL DEFAULT '',
			error         TEXT NOT NULL DEFAULT '',
			started_at    TEXT,
			finished_at   TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_orun_steps_run ON orchestration_run_steps(run_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV11 部署任务支持逐主机变量：任务级与主机级各存一份 params_json。
// 变量解析优先级：模板默认 < 任务默认 < 主机覆盖。
func migrateV11(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE deploy_tasks ADD COLUMN params_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE deploy_task_hosts ADD COLUMN params_json TEXT NOT NULL DEFAULT '{}'`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV10 主机唯一身份调整为 ip+port：name 退化为自动维护的显示字段
// （初始=IP，巡检后跟随系统 hostname）。清理历史重复项后建唯一索引，新旧库统一生效。
func migrateV10(db *sql.DB) error {
	// 同 ip+port 保留最早登记的一条（级联清理其部署记录/安装标记）
	if _, err := db.Exec(`DELETE FROM hosts WHERE id NOT IN (
		SELECT MIN(id) FROM hosts GROUP BY ip, port
	)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_hosts_ip_port ON hosts(ip, port)`)
	return err
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
