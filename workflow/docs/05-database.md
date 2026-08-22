# 05 — 数据库设计（SQLite）

## 1. 运行参数

- 驱动：modernc.org/sqlite（纯 Go，无 cgo）。
- 连接参数：`_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)`。
- 单写多读场景（巡检批量 UPDATE + API 低频写），WAL 足够；不引入连接池多写。
- db 文件位于 `data/infra-ops.db`，路径可配。

## 2. 迁移机制

- 表 `schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT)`。
- 代码内维护有序迁移列表（每条一个版本号 + DDL 函数），启动时自动执行未应用项
  （与 ops-platform 启动自动迁移同思路）。
- 迁移只增不改（rules.md §5.2）；阶段一包含版本 1（初始三表）。

## 3. 阶段一 DDL（迁移 v1）

```sql
CREATE TABLE IF NOT EXISTS credentials (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  name             TEXT NOT NULL UNIQUE,
  type             TEXT NOT NULL CHECK (type IN ('private_key','password')),
  username         TEXT NOT NULL DEFAULT 'root',
  encrypted_secret BLOB NOT NULL,            -- AES-256-GCM 密文(nonce|ciphertext|tag)
  fingerprint      TEXT NOT NULL DEFAULT '',  -- 私钥对应公钥指纹，仅展示用
  remark           TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL DEFAULT (datetime('now','localtime')),
  updated_at       TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE TABLE IF NOT EXISTS hosts (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT NOT NULL UNIQUE,
  ip            TEXT NOT NULL,
  port          INTEGER NOT NULL DEFAULT 22,
  role          TEXT NOT NULL DEFAULT 'other' CHECK (role IN ('nginx','harbor','k8s','other')),
  remark        TEXT NOT NULL DEFAULT '',
  credential_id INTEGER NOT NULL REFERENCES credentials(id),
  status        TEXT NOT NULL DEFAULT 'unverified' CHECK (status IN ('online','offline','unverified')),
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  info_json     TEXT NOT NULL DEFAULT '{}',   -- 最近一次采集快照
  last_check_at TEXT,
  created_at    TEXT NOT NULL DEFAULT (datetime('now','localtime')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_hosts_role   ON hosts(role);
CREATE INDEX IF NOT EXISTS idx_hosts_status ON hosts(status);

CREATE TABLE IF NOT EXISTS audit_logs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  action      TEXT NOT NULL,                  -- host.create 等，见 04-api.md §7
  target_type TEXT NOT NULL DEFAULT '',
  target_id   INTEGER NOT NULL DEFAULT 0,
  detail      TEXT NOT NULL DEFAULT '',       -- 脱敏后的关键字段快照
  remote_ip   TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action  ON audit_logs(action);

-- SSH host key TOFU 指纹库（安全红线 rules.md §3.5 的落点）
CREATE TABLE IF NOT EXISTS host_keys (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  addr        TEXT NOT NULL UNIQUE,           -- ip:port
  fingerprint TEXT NOT NULL,
  first_seen  TEXT NOT NULL DEFAULT (datetime('now','localtime')),
  last_seen   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
```

## 4. 二期预留表（仅规划，阶段一不建）

```sql
-- 任务：模板 × 目标集合，一次批量执行 = 一条 task + N 条 task_hosts
tasks(id, name, kind, template_id, params_json, status, created_by, created_at, finished_at)
task_hosts(id, task_id, host_id, status, exit_code, started_at, finished_at)
task_logs(id, task_host_id, ts, stream, line)   -- 实时日志流水，按月清理策略另议
templates(id, name, category, version, script_text, params_schema_json)
```

## 5. 访问层约定

- 每张表对应一个 repo 文件（host_repo.go 等），方法命名：
  Get/GetByID/List（带条件与分页）/Create/Update/Delete/Count。
- List 统一返回 `(items []model.X, total int64, err error)`。
- 时间字段以字符串透出，前端直接展示，不做时区换算（部署机时区即展示时区）。
