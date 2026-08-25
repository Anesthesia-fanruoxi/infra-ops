// 部署中心存取层：模板 CRUD 与任务执行记录。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"infra-ops/model"
)

// DeployRepo 部署模板与任务存取。
type DeployRepo struct{}

func NewDeployRepo() *DeployRepo { return &DeployRepo{} }

const tplCols = "id,name,description,script,variables,is_builtin,created_at,updated_at"

// ListTemplates 全量模板列表（数量小，不分页）。
func (r *DeployRepo) ListTemplates() ([]model.DeployTemplate, error) {
	rows, err := DB.Query("SELECT " + tplCols + " FROM deploy_templates ORDER BY is_builtin DESC, id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.DeployTemplate
	for rows.Next() {
		row := tplRow{}
		if err := scanTplRow(rows, &row); err != nil {
			return nil, err
		}
		items = append(items, *row.toModel())
	}
	return items, rows.Err()
}

// tplRow 数据库扫描中转结构：variables 用 string 接收，
// 因 database/sql 不支持将 string 直接 Scan 进命名切片类型 json.RawMessage。
type tplRow struct {
	ID          int64
	Name        string
	Description string
	Script      string
	Variables   string
	IsBuiltin   bool
	CreatedAt   string
	UpdatedAt   string
}

func (r tplRow) toModel() *model.DeployTemplate {
	return &model.DeployTemplate{
		ID: r.ID, Name: r.Name, Description: r.Description,
		Script: r.Script, Variables: json.RawMessage(r.Variables),
		IsBuiltin: r.IsBuiltin, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func scanTplRow(rows *sql.Rows, r *tplRow) error {
	return rows.Scan(&r.ID, &r.Name, &r.Description, &r.Script, &r.Variables,
		&r.IsBuiltin, &r.CreatedAt, &r.UpdatedAt)
}

// GetTemplate 按 ID 取模板。
func (r *DeployRepo) GetTemplate(id int64) (*model.DeployTemplate, error) {
	row := tplRow{}
	err := DB.QueryRow("SELECT "+tplCols+" FROM deploy_templates WHERE id=?", id).
		Scan(&row.ID, &row.Name, &row.Description, &row.Script, &row.Variables,
			&row.IsBuiltin, &row.CreatedAt, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

// CreateTemplate 新建模板。
func (r *DeployRepo) CreateTemplate(t *model.DeployTemplate) (int64, error) {
	res, err := DB.Exec(
		`INSERT INTO deploy_templates(name,description,script,variables,is_builtin) VALUES(?,?,?,?,0)`,
		t.Name, t.Description, t.Script, string(t.Variables),
	)
	if err != nil {
		return 0, fmt.Errorf("create template: %w", err)
	}
	return res.LastInsertId()
}

// UpdateTemplate 更新模板内容（内置模板仅允许改描述以外的场景由上层限制）。
func (r *DeployRepo) UpdateTemplate(t *model.DeployTemplate) error {
	_, err := DB.Exec(
		`UPDATE deploy_templates SET name=?, description=?, script=?, variables=?,
		updated_at=datetime('now','localtime') WHERE id=?`,
		t.Name, t.Description, t.Script, string(t.Variables), t.ID,
	)
	return err
}

// DeleteTemplate 删除模板。
func (r *DeployRepo) DeleteTemplate(id int64) error {
	_, err := DB.Exec("DELETE FROM deploy_templates WHERE id=?", id)
	return err
}

// CreateTask 事务创建任务与其全部主机记录（pending 态）。
func (r *DeployRepo) CreateTask(task *model.DeployTask, hosts []model.DeployTaskHost) (int64, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO deploy_tasks(template_id,template_name,status,total,schedule_id,trigger_type,params_json) VALUES(?,?,'running',?,?,?,?)`,
		task.TemplateID, task.TemplateName, len(hosts), task.ScheduleID, task.TriggerType, task.ParamsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("create task: %w", err)
	}
	taskID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, h := range hosts {
		if _, err := tx.Exec(
			`INSERT INTO deploy_task_hosts(task_id,host_id,host_name,host_ip,status,params_json) VALUES(?,?,?,?,'pending',?)`,
			taskID, h.HostID, h.HostName, h.HostIP, h.ParamsJSON,
		); err != nil {
			return 0, fmt.Errorf("create task host: %w", err)
		}
	}
	return taskID, tx.Commit()
}

// UpdateHostStatus 更新单台主机执行结果。
func (r *DeployRepo) UpdateHostStatus(recID int64, status, output, errMsg string) error {
	_, err := DB.Exec(
		`UPDATE deploy_task_hosts SET status=?, output=?, error=?,
		started_at=COALESCE(started_at, datetime('now','localtime')),
		finished_at=CASE WHEN ? IN ('success','failed') THEN datetime('now','localtime') ELSE finished_at END
		WHERE id=?`,
		status, output, errMsg, status, recID,
	)
	return err
}

// HostRecord 任务主机记录（含 ID 供状态回写）。
type HostRecord struct {
	model.DeployTaskHost
	RecID int64
}

// TaskHosts 取任务下全部主机记录。
func (r *DeployRepo) TaskHosts(taskID int64) ([]HostRecord, error) {
	rows, err := DB.Query(
		`SELECT id,host_id,host_name,host_ip,status,output,error,params_json FROM deploy_task_hosts WHERE task_id=? ORDER BY id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []HostRecord
	for rows.Next() {
		var h HostRecord
		if err := rows.Scan(&h.RecID, &h.HostID, &h.HostName, &h.HostIP, &h.Status, &h.Output, &h.Error, &h.ParamsJSON); err != nil {
			return nil, err
		}
		items = append(items, h)
	}
	return items, rows.Err()
}

// CleanupFinishedBefore 删除 finished_at 早于保留期的已结束任务（级联删除主机记录与日志输出）。
// 返回删除的任务数。
func (r *DeployRepo) CleanupFinishedBefore(days int) (int64, error) {
	res, err := DB.Exec(
		`DELETE FROM deploy_tasks
		WHERE finished_at IS NOT NULL
		AND finished_at < datetime('now','localtime', ?)`,
		fmt.Sprintf("-%d days", days),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkHostInstalled 标记主机成功安装过某模板（每模板一条，重复执行刷新任务号与时间）。
func (r *DeployRepo) MarkHostInstalled(hostID, templateID int64, templateName string, taskID int64) error {
	_, err := DB.Exec(
		`INSERT INTO host_installs(host_id,template_id,template_name,task_id) VALUES(?,?,?,?)
		ON CONFLICT(host_id,template_name) DO UPDATE SET
			template_id=excluded.template_id, task_id=excluded.task_id,
			updated_at=datetime('now','localtime')`,
		hostID, templateID, templateName, taskID,
	)
	if err != nil {
		return fmt.Errorf("mark host install: %w", err)
	}
	return nil
}

// HostInstalls 主机已执行过的安装记录（按最近执行倒序）。
func (r *DeployRepo) HostInstalls(hostID int64) ([]model.HostInstall, error) {
	rows, err := DB.Query(
		`SELECT id,host_id,template_id,template_name,task_id,created_at,updated_at
		FROM host_installs WHERE host_id=? ORDER BY updated_at DESC`, hostID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.HostInstall
	for rows.Next() {
		var it model.HostInstall
		if err := rows.Scan(&it.ID, &it.HostID, &it.TemplateID, &it.TemplateName, &it.TaskID, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// FinishTask 汇总成败计数并落任务终态。
func (r *DeployRepo) FinishTask(taskID int64) (string, error) {
	var successCnt, failCnt, total int
	if err := DB.QueryRow(
		`SELECT
			SUM(CASE WHEN status='success' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),
			COUNT(*)
		FROM deploy_task_hosts WHERE task_id=?`, taskID,
	).Scan(&successCnt, &failCnt, &total); err != nil {
		return "", err
	}

	status := "failed"
	switch {
	case failCnt == 0 && successCnt == total:
		status = "success"
	case successCnt > 0:
		status = "partial"
	}
	_, err := DB.Exec(
		`UPDATE deploy_tasks SET status=?, success_cnt=?, fail_cnt=?, finished_at=datetime('now','localtime') WHERE id=?`,
		status, successCnt, failCnt, taskID,
	)
	return status, err
}

// ListTasks 分页任务列表。
func (r *DeployRepo) ListTasks(page, pageSize int) ([]model.DeployTask, int64, error) {
	var total int64
	if err := DB.QueryRow("SELECT COUNT(*) FROM deploy_tasks").Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := DB.Query(
		`SELECT id,template_id,template_name,status,total,success_cnt,fail_cnt,schedule_id,trigger_type,created_at,finished_at
		FROM deploy_tasks ORDER BY id DESC LIMIT ? OFFSET ?`, pageSize, offset,
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

// GetTask 任务详情（不含主机明细，用 TaskHosts 查）。
func (r *DeployRepo) GetTask(id int64) (*model.DeployTask, error) {
	t := &model.DeployTask{}
	err := DB.QueryRow(
		`SELECT id,template_id,template_name,status,total,success_cnt,fail_cnt,schedule_id,trigger_type,created_at,finished_at
		FROM deploy_tasks WHERE id=?`, id,
	).Scan(&t.ID, &t.TemplateID, &t.TemplateName, &t.Status, &t.Total,
		&t.SuccessCnt, &t.FailCnt, &t.ScheduleID, &t.TriggerType, &t.CreatedAt, &t.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func scanTemplate(rows *sql.Rows, t *model.DeployTemplate) error {
	return rows.Scan(&t.ID, &t.Name, &t.Description, &t.Script, &t.Variables, &t.IsBuiltin, &t.CreatedAt, &t.UpdatedAt)
}
