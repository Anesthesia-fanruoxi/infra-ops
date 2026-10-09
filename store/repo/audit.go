package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/common/eventbus"
	"infra-ops/model"
	"infra-ops/store"
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

// AuditQuery 审计日志查询条件。
type AuditQuery struct {
	Action   string // 业务模块（target_type，如 hosts/credentials）精确匹配，空为全部
	Status   string // "" 全部 / "success" / "fail"（业务码非 0 或 HTTP >= 400 判定）
	Keyword  string // action/message 模糊匹配
	From     string // 起始时间 "YYYY-MM-DD HH:mm:ss"，空忽略
	To       string // 结束时间，空忽略
	Page     int
	PageSize int
}

// AuditStats 审计统计概览。
type AuditStats struct {
	TodayCount int64 `json:"today_count"`
	Fail24h    int64 `json:"fail_24h"`
	TotalCount int64 `json:"total_count"`
}

const auditCols = "id,action,target_type,target_id,http_status,code,message,created_at"

// failCond 失败判定：业务码非 0 或 HTTP 状态 >= 400。
const failCond = "(code<>0 OR http_status>=400)"

// Create 写入审计日志。
func (r *AuditRepo) Create(logEntry *model.AuditLog) error {
	// RETURNING 回读自增 ID 与落库时间，保证 SSE 实时推送携带完整字段
	err := store.DB.QueryRow(
		"INSERT INTO audit_logs(action,target_type,target_id,http_status,code,message) VALUES(?,?,?,?,?,?) RETURNING id, created_at",
		logEntry.Action, logEntry.TargetType, logEntry.TargetID, logEntry.HTTPStatus, logEntry.Code, logEntry.Message,
	).Scan(&logEntry.ID, &logEntry.CreatedAt)
	if err != nil {
		return err
	}

	if r.bus != nil {
		r.bus.Publish(eventbus.TopicAuditCreated, *logEntry)
	}
	return nil
}

// List 多条件分页查询审计日志。
func (r *AuditRepo) List(q AuditQuery) ([]model.AuditLog, int64, error) {
	where, args := buildAuditWhere(q)

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM audit_logs"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit count: %w", err)
	}

	offset := (q.Page - 1) * q.PageSize
	query := "SELECT " + auditCols + " FROM audit_logs" + where +
		" ORDER BY id DESC LIMIT ? OFFSET ?"
	queryArgs := append(args, q.PageSize, offset)

	rows, err := store.DB.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit list: %w", err)
	}
	defer rows.Close()

	items, err := scanAuditRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Stats 返回审计统计概览。
func (r *AuditRepo) Stats() (*AuditStats, error) {
	s := &AuditStats{}
	err := store.DB.QueryRow(
		`SELECT
			COALESCE(SUM(CASE WHEN date(created_at)=date('now','localtime') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN `+failCond+` AND created_at>=datetime('now','localtime','-24 hours') THEN 1 ELSE 0 END),0),
			COALESCE(COUNT(*),0)
		FROM audit_logs`,
	).Scan(&s.TodayCount, &s.Fail24h, &s.TotalCount)
	if err != nil {
		return nil, fmt.Errorf("audit stats: %w", err)
	}
	return s, nil
}

// Recent 获取最近 N 条审计日志。
func (r *AuditRepo) Recent(limit int) ([]model.AuditLog, error) {
	rows, err := store.DB.Query("SELECT "+auditCols+" FROM audit_logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// buildAuditWhere 拼接查询条件；失败判定 = 业务码非 0 或 HTTP 状态 >= 400。
func buildAuditWhere(q AuditQuery) (string, []interface{}) {
	var conds []string
	var args []interface{}

	if q.Action != "" {
		conds = append(conds, "target_type = ?")
		args = append(args, q.Action)
	}
	switch q.Status {
	case "fail":
		conds = append(conds, failCond)
	case "success":
		conds = append(conds, "NOT "+failCond)
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		conds = append(conds, "(action LIKE ? OR message LIKE ?)")
		kw = "%" + kw + "%"
		args = append(args, kw, kw)
	}
	if q.From != "" {
		conds = append(conds, "created_at >= ?")
		args = append(args, q.From)
	}
	if q.To != "" {
		conds = append(conds, "created_at <= ?")
		args = append(args, q.To)
	}

	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func scanAuditRows(rows *sql.Rows) ([]model.AuditLog, error) {
	var items []model.AuditLog
	for rows.Next() {
		var a model.AuditLog
		if err := rows.Scan(&a.ID, &a.Action, &a.TargetType, &a.TargetID, &a.HTTPStatus, &a.Code, &a.Message, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("audit scan: %w", err)
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
