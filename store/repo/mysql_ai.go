package repo

import (
	"encoding/json"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// MySQLAIRepo AI 语义目录（mysql_ai_catalog）的数据存取。
type MySQLAIRepo struct{}

func NewMySQLAIRepo() *MySQLAIRepo { return &MySQLAIRepo{} }

// ListCatalog 取某连接某库的全部目录条目。
func (r *MySQLAIRepo) ListCatalog(connID int64, schema string) ([]model.MySQLAICatalogEntry, error) {
	rows, err := store.DB.Query(
		`SELECT schema_name, table_name, purpose, columns_json, fingerprint, generated_at
		 FROM mysql_ai_catalog WHERE conn_id=? AND schema_name=? ORDER BY table_name`, connID, schema)
	if err != nil {
		return nil, fmt.Errorf("mysql ai catalog list: %w", err)
	}
	defer rows.Close()

	out := []model.MySQLAICatalogEntry{}
	for rows.Next() {
		var e model.MySQLAICatalogEntry
		var cols string
		if err := rows.Scan(&e.SchemaName, &e.TableName, &e.Purpose, &cols, &e.Fingerprint, &e.GeneratedAt); err != nil {
			return nil, fmt.Errorf("mysql ai catalog scan: %w", err)
		}
		if err := json.Unmarshal([]byte(cols), &e.Columns); err != nil || e.Columns == nil {
			e.Columns = []model.MySQLAIColumn{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListCatalogAll 取某连接全部库的目录条目（跨库生成 SQL 时用）。
func (r *MySQLAIRepo) ListCatalogAll(connID int64) ([]model.MySQLAICatalogEntry, error) {
	rows, err := store.DB.Query(
		`SELECT schema_name, table_name, purpose, columns_json, fingerprint, generated_at
		 FROM mysql_ai_catalog WHERE conn_id=? ORDER BY schema_name, table_name`, connID)
	if err != nil {
		return nil, fmt.Errorf("mysql ai catalog list all: %w", err)
	}
	defer rows.Close()

	out := []model.MySQLAICatalogEntry{}
	for rows.Next() {
		var e model.MySQLAICatalogEntry
		var cols string
		if err := rows.Scan(&e.SchemaName, &e.TableName, &e.Purpose, &cols, &e.Fingerprint, &e.GeneratedAt); err != nil {
			return nil, fmt.Errorf("mysql ai catalog scan: %w", err)
		}
		if err := json.Unmarshal([]byte(cols), &e.Columns); err != nil || e.Columns == nil {
			e.Columns = []model.MySQLAIColumn{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertCatalog 写入或覆盖一条目录（连接 + 库 + 表唯一）。
func (r *MySQLAIRepo) UpsertCatalog(connID int64, e *model.MySQLAICatalogEntry) error {
	cols, err := json.Marshal(e.Columns)
	if err != nil {
		return err
	}
	_, err = store.DB.Exec(
		`INSERT INTO mysql_ai_catalog(conn_id, schema_name, table_name, purpose, columns_json, fingerprint, generated_at)
		 VALUES(?,?,?,?,?,?,datetime('now','localtime'))
		 ON CONFLICT(conn_id, schema_name, table_name) DO UPDATE SET
		   purpose=excluded.purpose, columns_json=excluded.columns_json,
		   fingerprint=excluded.fingerprint, generated_at=excluded.generated_at`,
		connID, e.SchemaName, e.TableName, e.Purpose, string(cols), e.Fingerprint)
	if err != nil {
		return fmt.Errorf("mysql ai catalog upsert: %w", err)
	}
	return nil
}

// DropCatalogNotIn 清理该库下已不存在的表残留条目（表被删 / 改名后目录不留脏数据）。
func (r *MySQLAIRepo) DropCatalogNotIn(connID int64, schema string, keep []string) error {
	if len(keep) == 0 {
		_, err := store.DB.Exec(`DELETE FROM mysql_ai_catalog WHERE conn_id=? AND schema_name=?`, connID, schema)
		return err
	}
	args := []interface{}{connID, schema}
	for _, k := range keep {
		args = append(args, k)
	}
	q := `DELETE FROM mysql_ai_catalog WHERE conn_id=? AND schema_name=? AND table_name NOT IN (?` +
		strings.Repeat(",?", len(keep)-1) + `)`
	_, err := store.DB.Exec(q, args...)
	return err
}

// DropCatalogByConn 删除连接时一并清掉它的目录。
func (r *MySQLAIRepo) DropCatalogByConn(connID int64) error {
	_, err := store.DB.Exec(`DELETE FROM mysql_ai_catalog WHERE conn_id=?`, connID)
	return err
}
