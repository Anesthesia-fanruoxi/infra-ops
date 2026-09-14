package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// ESViewRepo 数据视图（es_views）的存取。仅负责落库，不含任何业务判断。
type ESViewRepo struct{}

func NewESViewRepo() *ESViewRepo { return &ESViewRepo{} }

const esViewCols = "id,conn_id,name,index_pattern,time_field,fields_json,stats_json,sync_status,sync_error,synced_at,created_at,updated_at"

// SyncUpdate 单视图同步结果，供 SaveSyncResults 单事务批量写入。
type SyncUpdate struct {
	ViewID     int64
	FieldsJSON string
	StatsJSON  string
	ErrMsg     string // 空 => 成功；非空 => 仅更新失败状态，快照保留
}

// ListByConn 某 ES 连接下的全部视图。
func (r *ESViewRepo) ListByConn(connID int64) ([]model.ESView, error) {
	rows, err := store.DB.Query(
		"SELECT "+esViewCols+" FROM es_views WHERE conn_id=? ORDER BY id ASC", connID)
	if err != nil {
		return nil, fmt.Errorf("es_view list: %w", err)
	}
	defer rows.Close()
	return scanESViews(rows)
}

// ListAll 全部视图（后台同步用，遍历所有连接）。
func (r *ESViewRepo) ListAll() ([]model.ESView, error) {
	rows, err := store.DB.Query("SELECT " + esViewCols + " FROM es_views ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("es_view list all: %w", err)
	}
	defer rows.Close()
	return scanESViews(rows)
}

// GetByID 按 ID 查询。
func (r *ESViewRepo) GetByID(id int64) (*model.ESView, error) {
	v := &model.ESView{}
	err := store.DB.QueryRow("SELECT "+esViewCols+" FROM es_views WHERE id=?", id).
		Scan(&v.ID, &v.ConnID, &v.Name, &v.IndexPattern, &v.TimeField,
			&v.FieldsJSON, &v.StatsJSON, &v.SyncStatus, &v.SyncError, &v.SyncedAt,
			&v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("es_view get: %w", err)
	}
	return v, nil
}

// GetByName 同连接内按名称查询（重名校验用）。
func (r *ESViewRepo) GetByName(connID int64, name string) (*model.ESView, error) {
	v := &model.ESView{}
	err := store.DB.QueryRow("SELECT "+esViewCols+" FROM es_views WHERE conn_id=? AND name=?", connID, name).
		Scan(&v.ID, &v.ConnID, &v.Name, &v.IndexPattern, &v.TimeField,
			&v.FieldsJSON, &v.StatsJSON, &v.SyncStatus, &v.SyncError, &v.SyncedAt,
			&v.CreatedAt, &v.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("es_view get by name: %w", err)
	}
	return v, nil
}

// Create 新增视图（初始状态 idle，同步成功由 SyncResult 写入字段表）。
func (r *ESViewRepo) Create(v *model.ESView) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO es_views(conn_id,name,index_pattern,time_field) VALUES(?,?,?,?)",
		v.ConnID, v.Name, v.IndexPattern, v.TimeField)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("duplicate name")
		}
		return 0, fmt.Errorf("es_view create: %w", err)
	}
	return res.LastInsertId()
}

// UpdateBasic 改名 / 改 pattern / 改时间字段。
func (r *ESViewRepo) UpdateBasic(id int64, name, pattern, timeField string) error {
	_, err := store.DB.Exec(
		"UPDATE es_views SET name=?,index_pattern=?,time_field=?,updated_at=datetime('now','localtime') WHERE id=?",
		name, pattern, timeField, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("duplicate name")
		}
		return fmt.Errorf("es_view update: %w", err)
	}
	return nil
}

// Delete 删除视图（不触碰 ES，外键级联由 SQLite 处理）。
func (r *ESViewRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM es_views WHERE id=?", id)
	return err
}

// MarkSyncing 启动同步时置 syncing 状态（用于手动刷新/单视图互斥）。
func (r *ESViewRepo) MarkSyncing(id int64) error {
	_, err := store.DB.Exec(
		"UPDATE es_views SET sync_status='syncing',updated_at=datetime('now','localtime') WHERE id=?", id)
	return err
}

// ResetSyncingAll 启动时把残留的 syncing 重置为 idle（进程崩溃自愈）。
func (r *ESViewRepo) ResetSyncingAll() error {
	_, err := store.DB.Exec("UPDATE es_views SET sync_status='idle',updated_at=datetime('now','localtime') WHERE sync_status='syncing'")
	return err
}

// SaveSyncResults 单事务批量写入一轮同步结果：成功视图整份替换快照，
// 失败视图仅落 failed 状态与错误，fields_json/stats_json/synced_at 原样保留。
func (r *ESViewRepo) SaveSyncResults(updates []SyncUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := store.DB.Begin()
	if err != nil {
		return fmt.Errorf("es_view sync begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, u := range updates {
		if u.ErrMsg == "" {
			_, err := tx.Exec(
				"UPDATE es_views SET fields_json=?,stats_json=?,sync_status='idle',sync_error='',synced_at=datetime('now','localtime'),updated_at=datetime('now','localtime') WHERE id=?",
				u.FieldsJSON, u.StatsJSON, u.ViewID)
			if err != nil {
				return fmt.Errorf("es_view sync success: %w", err)
			}
		} else {
			_, err := tx.Exec(
				"UPDATE es_views SET sync_status='failed',sync_error=?,updated_at=datetime('now','localtime') WHERE id=?",
				u.ErrMsg, u.ViewID)
			if err != nil {
				return fmt.Errorf("es_view sync fail: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("es_view sync commit: %w", err)
	}
	return nil
}

func scanESViews(rows *sql.Rows) ([]model.ESView, error) {
	var items []model.ESView
	for rows.Next() {
		var v model.ESView
		if err := rows.Scan(&v.ID, &v.ConnID, &v.Name, &v.IndexPattern, &v.TimeField,
			&v.FieldsJSON, &v.StatsJSON, &v.SyncStatus, &v.SyncError, &v.SyncedAt,
			&v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("es_view scan: %w", err)
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
