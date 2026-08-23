package store

import (
	"database/sql"
	"fmt"
	"strings"

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

// AuditQuery 审计日志查询条件。
type AuditQuery struct {
	Action   string // action 前缀匹配，空为全部
	Status   string // "" 全部 / "success" / "fail"（action 以 _fail 结尾判定）
	Keyword  string // detail/remote_ip 模糊匹配
	From     string // 起始时间 "YYYY-MM-DD HH:mm:ss"，空忽略
	To       string // 结束时间，空忽略
	Page     int
	PageSize int
}

// AuditStats 审计统计概览。
type AuditStats struct {
	TodayCount   int64 `json:"today_count"`
	FailLogin24h int64 `json:"fail_login_24h"`
	ActiveIPs    int64 `json:"active_ips"`
}

const auditCols = "id,action,target_type,target_id,detail,remote_ip,created_at"

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

// List 多条件分页查询审计日志。
func (r *AuditRepo) List(q AuditQuery) ([]model.AuditLog, int64, error) {
	where, args := buildAuditWhere(q)

	var total int64
	if err := DB.QueryRow("SELECT COUNT(*) FROM audit_logs"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit count: %w", err)
	}

	offset := (q.Page - 1) * q.PageSize
	query := "SELECT " + auditCols + " FROM audit_logs" + where +
		" ORDER BY id DESC LIMIT ? OFFSET ?"
	queryArgs := append(args, q.PageSize, offset)

	rows, err := DB.Query(query, queryArgs...)
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
	err := DB.QueryRow(
		`SELECT
			COALESCE(SUM(CASE WHEN date(created_at)=date('now','localtime') THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN action='auth.login_fail' AND created_at>=datetime('now','localtime','-24 hours') THEN 1 ELSE 0 END),0),
			COALESCE(COUNT(DISTINCT CASE WHEN created_at>=datetime('now','localtime','-24 hours') THEN remote_ip END),0)
		FROM audit_logs`,
	).Scan(&s.TodayCount, &s.FailLogin24h, &s.ActiveIPs)
	if err != nil {
		return nil, fmt.Errorf("audit stats: %w", err)
	}
	return s, nil
}

// Recent 获取最近 N 条审计日志。
func (r *AuditRepo) Recent(limit int) ([]model.AuditLog, error) {
	rows, err := DB.Query("SELECT "+auditCols+" FROM audit_logs ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// GetLatestByAction 获取最新一条指定 action 的日志（用于取 lastCheckAt 等）。
func (r *AuditRepo) GetLatestByAction(action string) (*model.AuditLog, error) {
	a := &model.AuditLog{}
	err := DB.QueryRow(
		"SELECT "+auditCols+" FROM audit_logs WHERE action=? ORDER BY id DESC LIMIT 1",
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

// buildAuditWhere 拼接查询条件；失败判定用 substr(action,-5)='_fail' 精确匹配后缀。
func buildAuditWhere(q AuditQuery) (string, []interface{}) {
	var conds []string
	var args []interface{}

	if q.Action != "" {
		conds = append(conds, "action LIKE ?")
		args = append(args, q.Action+"%")
	}
	switch q.Status {
	case "fail":
		conds = append(conds, "substr(action,-5)='_fail'")
	case "success":
		conds = append(conds, "substr(action,-5)<>'_fail'")
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		conds = append(conds, "(detail LIKE ? OR remote_ip LIKE ?)")
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
		if err := rows.Scan(&a.ID, &a.Action, &a.TargetType, &a.TargetID, &a.Detail, &a.RemoteIP, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("audit scan: %w", err)
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
