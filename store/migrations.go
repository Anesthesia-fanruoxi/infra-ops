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
	{13, migrateV13},
	{14, migrateV14},
	{15, migrateV15},
	{16, migrateV16},
	{17, migrateV17},
	{18, migrateV18},
	{19, migrateV19},
	{20, migrateV20},
	{21, migrateV21},
	{22, migrateV22},
	{23, migrateV23},
	{24, migrateV24},
	{25, migrateV25},
	{26, migrateV26},
	{27, migrateV27},
	{28, migrateV28},
	{29, migrateV29},
	{30, migrateV30},
	{31, migrateV31},
	{32, migrateV32},
	{33, migrateV33},
	{34, migrateV34},
	{35, migrateV35},
	{36, migrateV36},
}

// migrateV33 工具-MySQL：使用记录（两张互不相关的表）。
//
//	mysql_sql_logs —— 人在工具里执行 SQL 的记录，记语句类型、影响行数、耗时与成败；
//	mysql_ai_logs  —— AI 被调用的记录（语义目录按批各一条、生成 SQL 各一条），
//	                  记输入来源、生成结果与 token 用量。
//
// 两者各记各的，不做关联：一条 SQL 执行记录只说明「有人执行过这句 SQL」，
// 一条 AI 记录只说明「AI 被调用了这一次、花了这些 token」，AI 生成的语句是否被拿去执行
// 不作为记录维度。两张表都按保留期自动清理（并入部署历史的保留策略，见 main.runRetentionLoop）。
func migrateV33(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS mysql_sql_logs (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			conn_id          INTEGER NOT NULL DEFAULT 0,
			conn_name        TEXT NOT NULL DEFAULT '',
			schema_name      TEXT NOT NULL DEFAULT '',
			sql_text         TEXT NOT NULL DEFAULT '',
			statement_count  INTEGER NOT NULL DEFAULT 0,
			sql_type         TEXT NOT NULL DEFAULT '',
			statement_kind   TEXT NOT NULL DEFAULT '',
			mode             TEXT NOT NULL DEFAULT '',
			confirmed        INTEGER NOT NULL DEFAULT 0,
			row_count        INTEGER NOT NULL DEFAULT 0,
			affected_rows    INTEGER NOT NULL DEFAULT 0,
			elapsed_ms       INTEGER NOT NULL DEFAULT 0,
			status           TEXT NOT NULL DEFAULT '',
			error            TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mysql_sql_logs_created ON mysql_sql_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mysql_sql_logs_conn ON mysql_sql_logs(conn_id, id DESC)`,
		`CREATE TABLE IF NOT EXISTS mysql_ai_logs (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			conn_id           INTEGER NOT NULL DEFAULT 0,
			conn_name         TEXT NOT NULL DEFAULT '',
			schema_name       TEXT NOT NULL DEFAULT '',
			kind              TEXT NOT NULL DEFAULT '',
			mode              TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			base_url          TEXT NOT NULL DEFAULT '',
			batch_no          INTEGER NOT NULL DEFAULT 0,
			batch_total       INTEGER NOT NULL DEFAULT 0,
			table_count       INTEGER NOT NULL DEFAULT 0,
			question          TEXT NOT NULL DEFAULT '',
			sql_text          TEXT NOT NULL DEFAULT '',
			content           TEXT NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			token_source      TEXT NOT NULL DEFAULT '',
			elapsed_ms        INTEGER NOT NULL DEFAULT 0,
			status            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mysql_ai_logs_created ON mysql_ai_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mysql_ai_logs_conn ON mysql_ai_logs(conn_id, id DESC)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV34 工具-Redis：连接配置表，密码加密存储。
// ssh_host_id 指向 hosts 表，非 0 时先借该主机建 SSH 隧道（内网实例走跳板机场景）。
// 工具本身只读（只做 key 浏览与值预览），故没有 read_only 列。
func migrateV34(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS redis_conns (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			host             TEXT NOT NULL,
			port             INTEGER NOT NULL DEFAULT 6379,
			username         TEXT NOT NULL DEFAULT '',
			encrypted_secret BLOB,
			default_db       INTEGER NOT NULL DEFAULT 0,
			ssh_host_id      INTEGER NOT NULL DEFAULT 0,
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`)
	return err
}

// migrateV32 工具-MySQL：AI 语义目录。
// 按「连接 + 库 + 表」缓存 AI 依据表/字段注释归纳出的用途与字段含义，
// 供自然语言生成 SQL 时充当上下文；fingerprint 记录生成时的表结构，变了才重生成。
func migrateV32(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS mysql_ai_catalog (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			conn_id      INTEGER NOT NULL,
			schema_name  TEXT NOT NULL,
			table_name   TEXT NOT NULL,
			purpose      TEXT NOT NULL DEFAULT '',
			columns_json TEXT NOT NULL DEFAULT '[]',
			fingerprint  TEXT NOT NULL DEFAULT '',
			generated_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			UNIQUE(conn_id, schema_name, table_name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mysql_ai_catalog_conn ON mysql_ai_catalog(conn_id, schema_name)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV31 工具-MySQL：数据库连接配置表，密码加密存储。
// ssh_host_id 指向 hosts 表，非 0 时先借该主机建 SSH 隧道（内网库走跳板机场景）。
func migrateV31(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS mysql_conns (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			host             TEXT NOT NULL,
			port             INTEGER NOT NULL DEFAULT 3306,
			username         TEXT NOT NULL DEFAULT 'root',
			encrypted_secret BLOB,
			default_schema   TEXT NOT NULL DEFAULT '',
			charset          TEXT NOT NULL DEFAULT 'utf8mb4',
			read_only        INTEGER NOT NULL DEFAULT 1,
			ssh_host_id      INTEGER NOT NULL DEFAULT 0,
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`)
	return err
}

// migrateV30 操作日志去身份化：删除 remote_ip/detail 列（桌面单机无远端身份语义，
// 历史记录中的身份前缀随列删除一并清除），新增结果列 http_status/code/message，
// 记录「何时做了什么、是否报错、返回了什么」；同时清理已废弃的认证配置（auth.*）。
func migrateV30(db *sql.DB) error {
	if err := dropColumnIfExists(db, "audit_logs", "remote_ip"); err != nil {
		return err
	}
	if err := dropColumnIfExists(db, "audit_logs", "detail"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "audit_logs", "http_status", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "audit_logs", "code", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "audit_logs", "message", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM settings WHERE key LIKE 'auth.%'`)
	return err
}

// migrateV29 部署任务增加 hub_host_id：选中已部署 Docker Registry 的主机作为镜像源时记录，
// 0 表示直连拉取。镜像预热与目标机信任配置不落库（执行期行为，日志留痕）。
func migrateV29(db *sql.DB) error {
	return addColumnIfMissing(db, "deploy_tasks", "hub_host_id", `INTEGER NOT NULL DEFAULT 0`)
}

// migrateV28 部署资产：套件离线物料登记（上传 / 服务端代下），部署时引擎经 SFTP 分发到目标机；
// 文件本体落盘 data/assets/<key>/<version>/，表里只存元数据（大小、哈希、来源）。
func migrateV28(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS stack_assets (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		asset_key  TEXT NOT NULL,
		version    TEXT NOT NULL,
		file_name  TEXT NOT NULL,
		size_bytes INTEGER NOT NULL DEFAULT 0,
		sha256     TEXT NOT NULL DEFAULT '',
		source     TEXT NOT NULL DEFAULT 'upload',
		created_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
		UNIQUE(asset_key, version)
	)`)
	return err
}

// migrateV27 套件运行流水线步骤：一次运行的阶段序列与状态（引擎推进、SSE 推送、前端步骤条渲染）。
func migrateV27(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stack_run_steps (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id      INTEGER NOT NULL REFERENCES stack_runs(id) ON DELETE CASCADE,
			seq         INTEGER NOT NULL,
			key         TEXT NOT NULL,
			label       TEXT NOT NULL DEFAULT '',
			target      TEXT NOT NULL DEFAULT '',
			component   TEXT NOT NULL DEFAULT '',
			phase       TEXT NOT NULL DEFAULT 'node',
			status      TEXT NOT NULL DEFAULT 'pending',
			error       TEXT NOT NULL DEFAULT '',
			started_at  TEXT,
			finished_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stack_run_steps_run ON stack_run_steps(run_id, seq)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV26 数据视图（es_views）：字段表本地持久化 + 异步同步，字段表随时可由 ES 重算故存单 JSON 快照。
func migrateV26(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS es_views (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			conn_id       INTEGER NOT NULL REFERENCES es_conns(id) ON DELETE CASCADE,
			name          TEXT NOT NULL,
			index_pattern TEXT NOT NULL,
			time_field    TEXT NOT NULL DEFAULT '',
			fields_json   TEXT NOT NULL DEFAULT '[]',
			stats_json    TEXT NOT NULL DEFAULT '{}',
			sync_status   TEXT NOT NULL DEFAULT 'idle'
			              CHECK (sync_status IN ('idle','syncing','failed')),
			sync_error    TEXT NOT NULL DEFAULT '',
			synced_at     TEXT,
			created_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at    TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_es_views_conn_name ON es_views(conn_id, name)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV25 部署模板-tags：类型标签列（如 ["时序","监控"]），卡片小徽标展示，
// 与 category 并存：category 管分区筛选，tags 表达库型/用途等细分。
func migrateV25(db *sql.DB) error {
	return addColumnIfMissing(db, "deploy_templates", "tags", `TEXT NOT NULL DEFAULT '[]'`)
}

// migrateV24 套件-RolePlan：stack_instances 增 role_plan_json 列，
// 存部署前物化的角色计划（docs/角色物化设计.md §4.2），只保留最近一份。
func migrateV24(db *sql.DB) error {
	return addColumnIfMissing(db, "stack_instances", "role_plan_json", `TEXT NOT NULL DEFAULT ''`)
}

// migrateV23 工具-SFTP：SFTP 连接配置表，密码/私钥加密存储。
func migrateV23(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sftp_conns (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			host             TEXT NOT NULL,
			port             INTEGER NOT NULL DEFAULT 22,
			username         TEXT NOT NULL DEFAULT '',
			encrypted_secret BLOB,
			encrypted_key    BLOB,
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV21 工具-镜像仓库：连接 Docker Registry 的配置表，密码加密存储。
func migrateV21(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS registries (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			url              TEXT NOT NULL,
			insecure         INTEGER NOT NULL DEFAULT 0,
			auth_type        TEXT NOT NULL DEFAULT 'anonymous' CHECK (auth_type IN ('anonymous','basic')),
			username         TEXT NOT NULL DEFAULT '',
			encrypted_secret BLOB,
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV22 工具-Elasticsearch：连接 ES 集群的配置表，密码加密存储。
func migrateV22(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS es_conns (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			url              TEXT NOT NULL,
			insecure         INTEGER NOT NULL DEFAULT 0,
			auth_type        TEXT NOT NULL DEFAULT 'anonymous' CHECK (auth_type IN ('anonymous','basic')),
			username         TEXT NOT NULL DEFAULT '',
			encrypted_secret BLOB,
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV17 deploy_templates 增加 configs 列：声明可被用户覆盖的配置文件（JSON 数组）。
func migrateV17(db *sql.DB) error {
	return addColumnIfMissing(db, "deploy_templates", "configs", `TEXT NOT NULL DEFAULT '[]'`)
}

// migrateV18 套件部署运行记录：蓝图在代码中，这里只存每次执行。
func migrateV18(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stack_runs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			stack_key   TEXT NOT NULL,
			stack_name  TEXT NOT NULL,
			mode        TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'running',
			total       INTEGER NOT NULL DEFAULT 0,
			success_cnt INTEGER NOT NULL DEFAULT 0,
			fail_cnt    INTEGER NOT NULL DEFAULT 0,
			params_json TEXT NOT NULL DEFAULT '{}',
			created_at  TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			finished_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS stack_run_hosts (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id            INTEGER NOT NULL REFERENCES stack_runs(id) ON DELETE CASCADE,
			host_id           INTEGER NOT NULL,
			host_name         TEXT NOT NULL,
			host_ip           TEXT NOT NULL,
			role              TEXT NOT NULL DEFAULT '',
			seq               INTEGER NOT NULL,
			params_json       TEXT NOT NULL DEFAULT '{}',
			status            TEXT NOT NULL DEFAULT 'pending',
			prereq_status     TEXT NOT NULL DEFAULT 'pending',
			node_status       TEXT NOT NULL DEFAULT 'pending',
			bootstrap_status  TEXT NOT NULL DEFAULT 'skipped',
			output            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			started_at        TEXT,
			finished_at       TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stack_run_hosts_run ON stack_run_hosts(run_id, seq)`,
		`CREATE TABLE IF NOT EXISTS stack_run_logs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id     INTEGER NOT NULL REFERENCES stack_runs(id) ON DELETE CASCADE,
			phase      TEXT NOT NULL DEFAULT '',
			host_id    INTEGER NOT NULL DEFAULT 0,
			host_ip    TEXT NOT NULL DEFAULT '',
			text       TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_stack_run_logs_run ON stack_run_logs(run_id, id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV19 套件集群实例：长期记录成员与组件，运行记录挂到实例上支持扩容/缩容/加装。
func migrateV19(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS stack_instances (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL,
			stack_key        TEXT NOT NULL,
			stack_name       TEXT NOT NULL,
			mode             TEXT NOT NULL,
			category         TEXT NOT NULL DEFAULT 'service',
			status           TEXT NOT NULL DEFAULT 'ready',
			params_json      TEXT NOT NULL DEFAULT '{}',
			components_json  TEXT NOT NULL DEFAULT '[]',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE TABLE IF NOT EXISTS stack_instance_hosts (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id      INTEGER NOT NULL REFERENCES stack_instances(id) ON DELETE CASCADE,
			host_id          INTEGER NOT NULL,
			host_name        TEXT NOT NULL,
			host_ip          TEXT NOT NULL,
			role             TEXT NOT NULL DEFAULT 'node',
			seq              INTEGER NOT NULL DEFAULT 0,
			params_json      TEXT NOT NULL DEFAULT '{}',
			components_json  TEXT NOT NULL DEFAULT '[]',
			status           TEXT NOT NULL DEFAULT 'active'
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_stack_instance_hosts_uniq ON stack_instance_hosts(instance_id, host_id)`,
		`CREATE INDEX IF NOT EXISTS idx_stack_instances_key ON stack_instances(stack_key, status)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	if err := addColumnIfMissing(db, "stack_runs", "instance_id", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	return addColumnIfMissing(db, "stack_runs", "op", `TEXT NOT NULL DEFAULT 'create'`)
}

// migrateV20 host_services 增加 instance_id 列：将服务记录关联到套件集群实例。
func migrateV20(db *sql.DB) error {
	return addColumnIfMissing(db, "host_services", "instance_id", `INTEGER NOT NULL DEFAULT 0`)
}

// migrateV16 部署任务运行时日志表：按行落库，供日志抽屉快照回放与实时追加；任务删除时级联清理。
func migrateV16(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS deploy_task_logs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id    INTEGER NOT NULL REFERENCES deploy_tasks(id) ON DELETE CASCADE,
			host_id    INTEGER NOT NULL DEFAULT 0,
			host_ip    TEXT NOT NULL DEFAULT '',
			text       TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deploy_task_logs_task ON deploy_task_logs(task_id, id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV15 服务清单与模板扩展：deploy_templates 增 services/category/requires；新增 host_services。
// 服务由模板声明、主机安装成功后自动登记，web=true 可前端一键打开。
func migrateV15(db *sql.DB) error {
	if err := addColumnIfMissing(db, "deploy_templates", "services", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "deploy_templates", "category", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "deploy_templates", "requires", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS host_services (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			host_id     INTEGER NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
			host_ip     TEXT NOT NULL DEFAULT '',
			service_name TEXT NOT NULL,
			url         TEXT NOT NULL DEFAULT '',
			web         INTEGER NOT NULL DEFAULT 0,
			template_id INTEGER NOT NULL DEFAULT 0,
			created_at  TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at  TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			UNIQUE(host_id, service_name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_host_services_host ON host_services(host_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// addColumnIfMissing 幂等为表新增一列（SQLite 无 IF NOT EXISTS for column）。
func addColumnIfMissing(db *sql.DB, table, col, ddl string) error {
	rows, err := db.Query(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col)
	if err != nil {
		return err
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return err
		}
	}
	if n == 0 {
		_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + ddl)
		return err
	}
	return nil
}

// dropColumnIfExists 幂等删除表列（SQLite 3.35+ 支持 ALTER TABLE DROP COLUMN，
// 仅当列未被索引/约束引用时可用）。
func dropColumnIfExists(db *sql.DB, table, col string) error {
	rows, err := db.Query(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col)
	if err != nil {
		return err
	}
	defer rows.Close()
	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return err
		}
	}
	if n > 0 {
		_, err = db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN ` + col)
		return err
	}
	return nil
}

// migrateV14 运行日志表：按步骤落库，供详情流快照重放；run 删除时级联清理。
func migrateV14(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS orchestration_run_logs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id     INTEGER NOT NULL REFERENCES orchestration_runs(id) ON DELETE CASCADE,
			seq        INTEGER NOT NULL,
			host_id    INTEGER NOT NULL,
			host_ip    TEXT NOT NULL DEFAULT '',
			text       TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_orch_run_logs ON orchestration_run_logs(run_id, seq, id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
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

// migrateV13 编排定义绑定目标主机（by_step 共享主机集；by_host 由主机链隐含）。
func migrateV13(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE orchestrations ADD COLUMN host_ids TEXT NOT NULL DEFAULT '[]'`)
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

// migrateV35 工具-监控查询：Prometheus / VictoriaMetrics 连接配置表 + AI 调用记录表。
// 指标数据都在远端、本地不落样本，故连接表只存端点与认证材料（密码 / Bearer Token 加密存储）；
// metrics_ai_logs 记 AI 生成 PromQL 与解读查询结果的调用（含 token 用量），按保留期自动清理。
func migrateV35(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS metrics_conns (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			name             TEXT NOT NULL UNIQUE,
			kind             TEXT NOT NULL DEFAULT 'prometheus',
			url              TEXT NOT NULL,
			insecure         INTEGER NOT NULL DEFAULT 0,
			auth_type        TEXT NOT NULL DEFAULT 'none',
			username         TEXT NOT NULL DEFAULT '',
			encrypted_secret BLOB,
			remark           TEXT NOT NULL DEFAULT '',
			created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
			updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE TABLE IF NOT EXISTS metrics_ai_logs (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			conn_id           INTEGER NOT NULL DEFAULT 0,
			conn_name         TEXT NOT NULL DEFAULT '',
			kind              TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			base_url          TEXT NOT NULL DEFAULT '',
			metric_count      INTEGER NOT NULL DEFAULT 0,
			question          TEXT NOT NULL DEFAULT '',
			expr              TEXT NOT NULL DEFAULT '',
			content           TEXT NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			token_source      TEXT NOT NULL DEFAULT '',
			elapsed_ms        INTEGER NOT NULL DEFAULT 0,
			status            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_ai_logs_created ON metrics_ai_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_ai_logs_conn ON metrics_ai_logs(conn_id, id DESC)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// migrateV36 统一 AI 调用记录：把两张各工具自建的 AI 记录表（mysql_ai_logs /
// metrics_ai_logs）合并为一张 ai_logs，用 menu 区分归属工具。
//
// menu 存页面 id（metrics / mysql），展示名由前端路由表映射 —— 页面名称会改
// （如「操作日志」→「审计日志」），id 才是稳定的关联键（与 audit_logs.target_type
// 存页面 id 是同一口径）。旧数据原样搬迁（保留 created_at）；两张旧表的 id 各自从 1
// 起会撞，故不带 id 搬、由新表自增重排，表内按 id 升序保持先后。
// 搬迁与删表同事务，中途失败重跑不会重复搬。
func migrateV36(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ai_logs (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			menu              TEXT NOT NULL DEFAULT '',
			conn_id           INTEGER NOT NULL DEFAULT 0,
			conn_name         TEXT NOT NULL DEFAULT '',
			schema_name       TEXT NOT NULL DEFAULT '',
			kind              TEXT NOT NULL DEFAULT '',
			mode              TEXT NOT NULL DEFAULT '',
			model             TEXT NOT NULL DEFAULT '',
			base_url          TEXT NOT NULL DEFAULT '',
			batch_no          INTEGER NOT NULL DEFAULT 0,
			batch_total       INTEGER NOT NULL DEFAULT 0,
			table_count       INTEGER NOT NULL DEFAULT 0,
			metric_count      INTEGER NOT NULL DEFAULT 0,
			question          TEXT NOT NULL DEFAULT '',
			expr              TEXT NOT NULL DEFAULT '',
			content           TEXT NOT NULL DEFAULT '',
			prompt_tokens     INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens      INTEGER NOT NULL DEFAULT 0,
			token_source      TEXT NOT NULL DEFAULT '',
			elapsed_ms        INTEGER NOT NULL DEFAULT 0,
			status            TEXT NOT NULL DEFAULT '',
			error             TEXT NOT NULL DEFAULT '',
			created_at        TEXT NOT NULL DEFAULT (datetime('now','localtime'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_logs_created ON ai_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_logs_menu ON ai_logs(menu, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_logs_conn ON ai_logs(conn_id, id DESC)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}

	// 列取两张旧表的并集：mysql 独有 schema_name / mode / batch_* / table_count
	// （sql_text 映射到 expr），metrics 独有 metric_count（expr 本就同名），缺的列补零值。
	candidates := []struct {
		table string
		stmt  string
	}{
		{"mysql_ai_logs", `INSERT INTO ai_logs(menu, conn_id, conn_name, schema_name, kind, mode, model, base_url,
				batch_no, batch_total, table_count, metric_count, question, expr, content,
				prompt_tokens, completion_tokens, total_tokens, token_source, elapsed_ms, status, error, created_at)
			SELECT 'mysql', conn_id, conn_name, schema_name, kind, mode, model, base_url,
				batch_no, batch_total, table_count, 0, question, sql_text, content,
				prompt_tokens, completion_tokens, total_tokens, token_source, elapsed_ms, status, error, created_at
			FROM mysql_ai_logs ORDER BY id`},
		{"metrics_ai_logs", `INSERT INTO ai_logs(menu, conn_id, conn_name, schema_name, kind, mode, model, base_url,
				batch_no, batch_total, table_count, metric_count, question, expr, content,
				prompt_tokens, completion_tokens, total_tokens, token_source, elapsed_ms, status, error, created_at)
			SELECT 'metrics', conn_id, conn_name, '', kind, '', model, base_url,
				0, 0, 0, metric_count, question, expr, content,
				prompt_tokens, completion_tokens, total_tokens, token_source, elapsed_ms, status, error, created_at
			FROM metrics_ai_logs ORDER BY id`},
	}
	var moves []struct {
		table string
		stmt  string
	}
	for _, c := range candidates {
		ok, err := tableExists(db, c.table)
		if err != nil {
			return err
		}
		if ok { // 历史库必然存在；表被人为删过就跳过，不让迁移失败
			moves = append(moves, c)
		}
	}
	if len(moves) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range moves {
		if _, err := tx.Exec(m.stmt); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TABLE ` + m.table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// tableExists 判断表是否存在（迁移内部用：旧库结构存在差异时按需决定动作）。
func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
