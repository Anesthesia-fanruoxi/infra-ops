package store

import (
	"database/sql"
	"fmt"

	"infra-ops/common/eventbus"
	"infra-ops/model"
)

// AuditRepo 审计日志存取。
type AuditRepo struct {
	bus *eventbus.Bus
}

// NewAuditRepo 创建审计日志仓库。bus 可选，用于通知实时订阅者。
func NewAuditRepo(bus ...*eventbus.Bus) *AuditRepo {
	repo := &AuditRepo{}
	if len(bus) > 0 {
		repo.bus = bus[0]
	}
	return repo
}

// Create 写入审计日志。
func (r *AuditRepo) Create(logEntry *model.AuditLog) error {
	res, err := DB.Exec(
		"INSERT INTO audit_logs(action,target_type,target_id,detail,remote_ip) VALUES(?,?,?,?,?)",
		logEntry.Action, logEntry.TargetType, logEntry.TargetID, logEntry.Detail, logEntry.RemoteIP,
	)
	if err != nil {
		return err
	}

	if id, err := res.LastInsertId(); err == nil {
		logEntry.ID = id
	}
	if r.bus != nil {
		r.bus.Publish(eventbus.TopicAuditCreated, *logEntry)
	}
	return nil
}

// List 分页查询审计日志，支持 action 前缀匹配。
func (r *AuditRepo) List(action string, page, pageSize int) ([]model.AuditLog, int64, error) {
	where := ""
	args := []interface{}{}
	if action != "" {
		where = "WHERE action LIKE ?"
		args = append(args, action+"%")
	}

	var total int64
	if err := DB.QueryRow("SELECT COUNT(*) FROM audit_logs "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit count: %w", err)
	}

	offset := (page - 1) * pageSize
	query := "SELECT id,action,target_type,target_id,detail,remote_ip,created_at FROM audit_logs " + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	queryArgs := append(args, pageSize, offset)

	rows, err := DB.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit list: %w", err)
	}
	defer rows.Close()

	var items []model.AuditLog
	for rows.Next() {
		var a model.AuditLog
		if err := rows.Scan(&a.ID, &a.Action, &a.TargetType, &a.TargetID, &a.Detail, &a.RemoteIP, &a.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("audit scan: %w", err)
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}

// Recent 获取最近 N 条审计日志。
func (r *AuditRepo) Recent(limit int) ([]model.AuditLog, error) {
	rows, err := DB.Query("SELECT id,action,target_type,target_id,detail,remote_ip,created_at FROM audit_logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.AuditLog
	for rows.Next() {
		var a model.AuditLog
		if err := rows.Scan(&a.ID, &a.Action, &a.TargetType, &a.TargetID, &a.Detail, &a.RemoteIP, &a.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// GetLatestByAction 获取最新一条指定 action 的日志（用于取 lastCheckAt 等）。
func (r *AuditRepo) GetLatestByAction(action string) (*model.AuditLog, error) {
	a := &model.AuditLog{}
	err := DB.QueryRow(
		"SELECT id,action,target_type,target_id,detail,remote_ip,created_at FROM audit_logs WHERE action=? ORDER BY id DESC LIMIT 1",
		action,
	).Scan(&a.ID, &a.Action, &a.TargetType, &a.TargetID, &a.Detail, &a.RemoteIP, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}
