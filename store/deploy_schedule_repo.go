// 定时任务存取层。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"infra-ops/model"
)

// DeployScheduleRepo 定时任务存取。
type DeployScheduleRepo struct{}

func NewDeployScheduleRepo() *DeployScheduleRepo { return &DeployScheduleRepo{} }

const schedCols = `id,name,template_id,host_ids,params_json,cron_expr,enabled,last_task_id,last_run_at,next_run_at,created_at,updated_at`

// schedRow 数据库扫描中转结构：JSON 列用 string 接收，
// 规避 database/sql 不支持 string 直接 Scan 进 json.RawMessage 的限制。
type schedRow struct {
	ID         int64
	Name       string
	TemplateID int64
	HostIDs    string
	Params     string
	CronExpr   string
	Enabled    bool
	LastTaskID sql.NullInt64
	LastRunAt  sql.NullString
	NextRunAt  sql.NullString
	CreatedAt  string
	UpdatedAt  string
}

func (r schedRow) toModel() *model.DeploySchedule {
	return &model.DeploySchedule{
		ID: r.ID, Name: r.Name, TemplateID: r.TemplateID,
		HostIDs: json.RawMessage(r.HostIDs), Params: json.RawMessage(r.Params),
		CronExpr: r.CronExpr, Enabled: r.Enabled, LastTaskID: r.LastTaskID.Int64,
		LastRunAt: nullStrPtr(r.LastRunAt), NextRunAt: nullStrPtr(r.NextRunAt),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func scanSchedRow(rows *sql.Rows, r *schedRow) error {
	return rows.Scan(&r.ID, &r.Name, &r.TemplateID, &r.HostIDs, &r.Params,
		&r.CronExpr, &r.Enabled, &r.LastTaskID, &r.LastRunAt, &r.NextRunAt,
		&r.CreatedAt, &r.UpdatedAt)
}

func nullStrPtr(ns sql.NullString) *string {
	if ns.Valid {
		s := ns.String
		return &s
	}
	return nil
}

// ListSchedules 全量定时任务列表。
func (r *DeployScheduleRepo) ListSchedules() ([]model.DeploySchedule, error) {
	rows, err := DB.Query("SELECT " + schedCols + " FROM deploy_schedules ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.DeploySchedule
	for rows.Next() {
		row := schedRow{}
		if err := scanSchedRow(rows, &row); err != nil {
			return nil, err
		}
		items = append(items, *row.toModel())
	}
	return items, rows.Err()
}

// GetSchedule 按 ID 取定时任务。
func (r *DeployScheduleRepo) GetSchedule(id int64) (*model.DeploySchedule, error) {
	row := schedRow{}
	err := DB.QueryRow("SELECT "+schedCols+" FROM deploy_schedules WHERE id=?", id).
		Scan(&row.ID, &row.Name, &row.TemplateID, &row.HostIDs, &row.Params,
			&row.CronExpr, &row.Enabled, &row.LastTaskID, &row.LastRunAt,
			&row.NextRunAt, &row.CreatedAt, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

// CreateSchedule 新建定时任务。
func (r *DeployScheduleRepo) CreateSchedule(s *model.DeploySchedule) (int64, error) {
	res, err := DB.Exec(
		`INSERT INTO deploy_schedules(name,template_id,host_ids,params_json,cron_expr,enabled) VALUES(?,?,?,?,?,?)`,
		s.Name, s.TemplateID, string(s.HostIDs), string(s.Params), s.CronExpr, s.Enabled,
	)
	if err != nil {
		return 0, fmt.Errorf("create schedule: %w", err)
	}
	return res.LastInsertId()
}

// UpdateSchedule 编辑定时任务（名称/模板/主机/参数/cron）。
func (r *DeployScheduleRepo) UpdateSchedule(s *model.DeploySchedule) error {
	_, err := DB.Exec(
		`UPDATE deploy_schedules SET name=?, template_id=?, host_ids=?, params_json=?, cron_expr=?,
		updated_at=datetime('now','localtime') WHERE id=?`,
		s.Name, s.TemplateID, string(s.HostIDs), string(s.Params), s.CronExpr, s.ID,
	)
	return err
}

// DeleteSchedule 删除定时任务。
func (r *DeployScheduleRepo) DeleteSchedule(id int64) error {
	_, err := DB.Exec("DELETE FROM deploy_schedules WHERE id=?", id)
	return err
}

// SetEnabled 启用/停用。
func (r *DeployScheduleRepo) SetEnabled(id int64, enabled bool) error {
	_, err := DB.Exec(`UPDATE deploy_schedules SET enabled=?, updated_at=datetime('now','localtime') WHERE id=?`, enabled, id)
	return err
}

// UpdateRunInfo 触发后回写最近任务与时间。
func (r *DeployScheduleRepo) UpdateRunInfo(id, lastTaskID int64) error {
	_, err := DB.Exec(
		`UPDATE deploy_schedules SET last_task_id=?, last_run_at=datetime('now','localtime') WHERE id=?`,
		lastTaskID, id,
	)
	return err
}

// SetNextRun 回写下次触发时间（展示用）。
func (r *DeployScheduleRepo) SetNextRun(id int64, next string) error {
	_, err := DB.Exec(`UPDATE deploy_schedules SET next_run_at=? WHERE id=?`, next, id)
	return err
}

// HasRunningTaskForSchedule 该定时任务是否存在未完成的执行（防堆积）。
func (r *DeployScheduleRepo) HasRunningTaskForSchedule(scheduleID int64) (bool, error) {
	var count int
	err := DB.QueryRow(
		`SELECT COUNT(*) FROM deploy_tasks WHERE schedule_id=? AND status='running'`, scheduleID,
	).Scan(&count)
	return count > 0, err
}

// CountByTemplate 统计引用某模板的定时任务数（删除保护用）。
func (r *DeployScheduleRepo) CountByTemplate(templateID int64) (int64, error) {
	var count int64
	err := DB.QueryRow(`SELECT COUNT(*) FROM deploy_schedules WHERE template_id=?`, templateID).Scan(&count)
	return count, err
}

// ListTasksBySchedule 某定时任务的历次触发记录（分页）。
func (r *DeployScheduleRepo) ListTasksBySchedule(scheduleID int64, page, pageSize int) ([]model.DeployTask, int64, error) {
	var total int64
	if err := DB.QueryRow(`SELECT COUNT(*) FROM deploy_tasks WHERE schedule_id=?`, scheduleID).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := DB.Query(
		`SELECT id,template_id,template_name,status,total,success_cnt,fail_cnt,schedule_id,trigger_type,created_at,finished_at
		FROM deploy_tasks WHERE schedule_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		scheduleID, pageSize, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []model.DeployTask
	for rows.Next() {
		var t model.DeployTask
		if err := rows.Scan(&t.ID, &t.TemplateID, &t.TemplateName, &t.Status, &t.Total,
			&t.SuccessCnt, &t.FailCnt, &t.ScheduleID, &t.TriggerType, &t.CreatedAt, &t.FinishedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, t)
	}
	return items, total, rows.Err()
}
