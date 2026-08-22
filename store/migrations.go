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
