// 编排存取层：定义/步骤 CRUD 与运行记录。
package store

import (
	"database/sql"
	"fmt"

	"infra-ops/model"
)

// OrchestrationRepo 任务编排存取。
type OrchestrationRepo struct{}

func NewOrchestrationRepo() *OrchestrationRepo { return &OrchestrationRepo{} }

const orchCols = `o.id,o.name,o.description,o.exec_mode,o.enabled,o.created_at,o.updated_at,
	(SELECT COUNT(*) FROM orchestration_steps s WHERE s.orchestration_id=o.id) AS step_count`

// List 全量编排列表（数量小，不分页）。
func (r *OrchestrationRepo) List() ([]model.Orchestration, error) {
	rows, err := DB.Query(`SELECT ` + orchCols + ` FROM orchestrations o ORDER BY o.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.Orchestration
	for rows.Next() {
		var o model.Orchestration
		if err := rows.Scan(&o.ID, &o.Name, &o.Description, &o.ExecMode, &o.Enabled,
			&o.CreatedAt, &o.UpdatedAt, &o.StepCount); err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return items, rows.Err()
}

// Get 编排详情（含步骤与步骤主机变量）。
func (r *OrchestrationRepo) Get(id int64) (*model.Orchestration, []model.OrchestrationStep, []model.OrchestrationStepVar, error) {
	var o model.Orchestration
	err := DB.QueryRow(`SELECT `+orchCols+` FROM orchestrations o WHERE o.id=?`, id).
		Scan(&o.ID, &o.Name, &o.Description, &o.ExecMode, &o.Enabled, &o.CreatedAt, &o.UpdatedAt, &o.StepCount)
	if err == sql.ErrNoRows {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	rows, err := DB.Query(`
		SELECT s.id,s.orchestration_id,s.seq,s.template_id,t.name,s.params_json,s.host_scope,
			s.continue_on_error,s.retry_count,s.retry_interval_sec,s.timeout_sec
		FROM orchestration_steps s JOIN deploy_templates t ON t.id=s.template_id
		WHERE s.orchestration_id=? ORDER BY s.seq`, id)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	steps := []model.OrchestrationStep{}
	for rows.Next() {
		var st model.OrchestrationStep
		var cont int
		if err := rows.Scan(&st.ID, &st.OrchestrationID, &st.Seq, &st.TemplateID, &st.TemplateName,
			&st.ParamsJSON, &st.HostScope, &cont, &st.RetryCount, &st.RetryIntervalSec, &st.TimeoutSec); err != nil {
			return nil, nil, nil, err
		}
		st.ContinueOnError = cont == 1
		steps = append(steps, st)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	svars, err := r.stepVars(id)
	if err != nil {
		return nil, nil, nil, err
	}
	return &o, steps, svars, nil
}

// stepVars 步骤主机级变量覆盖。
func (r *OrchestrationRepo) stepVars(id int64) ([]model.OrchestrationStepVar, error) {
	rows, err := DB.Query(
		`SELECT seq,host_id,params_json FROM orchestration_step_host_vars WHERE orchestration_id=? ORDER BY seq,host_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	svars := []model.OrchestrationStepVar{}
	for rows.Next() {
		var sv model.OrchestrationStepVar
		if err := rows.Scan(&sv.Seq, &sv.HostID, &sv.ParamsJSON); err != nil {
			return nil, err
		}
		svars = append(svars, sv)
	}
	return svars, rows.Err()
}

// Save 新建或更新编排（步骤/步骤变量全量替换）。返回编排 ID。
func (r *OrchestrationRepo) Save(o *model.Orchestration, steps []model.OrchestrationStep,
	stepVars []model.OrchestrationStepVar) (int64, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	enabled := 0
	if o.Enabled {
		enabled = 1
	}
	if o.ID > 0 {
		res, err := tx.Exec(
			`UPDATE orchestrations SET name=?,description=?,exec_mode=?,enabled=?,updated_at=datetime('now','localtime') WHERE id=?`,
			o.Name, o.Description, o.ExecMode, enabled, o.ID)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, sql.ErrNoRows
		}
		if _, err := tx.Exec(`DELETE FROM orchestration_steps WHERE orchestration_id=?`, o.ID); err != nil {
			return 0, err
		}
	} else {
		res, err := tx.Exec(
			`INSERT INTO orchestrations(name,description,exec_mode,enabled) VALUES(?,?,?,?)`,
			o.Name, o.Description, o.ExecMode, enabled)
		if err != nil {
			return 0, fmt.Errorf("create orchestration: %w", err)
		}
		o.ID, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	}
	for i := range steps {
		st := steps[i]
		cont := 0
		if st.ContinueOnError {
			cont = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO orchestration_steps(orchestration_id,seq,template_id,params_json,host_scope,continue_on_error,retry_count,retry_interval_sec,timeout_sec)
			 VALUES(?,?,?,?,?,?,?,?,?)`,
			o.ID, i+1, st.TemplateID, orDefaultJSON(st.ParamsJSON), orDefaultJSON(st.HostScope), cont,
			st.RetryCount, st.RetryIntervalSec, st.TimeoutSec); err != nil {
			return 0, fmt.Errorf("save step %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM orchestration_step_host_vars WHERE orchestration_id=?`, o.ID); err != nil {
		return 0, err
	}
	for _, sv := range stepVars {
		if _, err := tx.Exec(
			`INSERT INTO orchestration_step_host_vars(orchestration_id,seq,host_id,params_json) VALUES(?,?,?,?)`,
			o.ID, sv.Seq, sv.HostID, orDefaultJSON(sv.ParamsJSON)); err != nil {
			return 0, fmt.Errorf("save step var seq=%d host=%d: %w", sv.Seq, sv.HostID, err)
		}
	}
	return o.ID, tx.Commit()
}

// Delete 删除编排（级联步骤）。
func (r *OrchestrationRepo) Delete(id int64) error {
	_, err := DB.Exec(`DELETE FROM orchestrations WHERE id=?`, id)
	return err
}

func orDefaultJSON(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

// CreateRun 建运行实例并批量写入全部 pending 明细。
func (r *OrchestrationRepo) CreateRun(run *model.OrchestrationRun, steps []model.OrchestrationRunStep) (int64, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO orchestration_runs(orchestration_id,name,exec_mode,status,total_hosts,trigger_type,host_ids)
		 VALUES(?,?,?,'running',?,?,?)`,
		run.OrchestrationID, run.Name, run.ExecMode, run.TotalHosts, run.TriggerType, run.HostIDs)
	if err != nil {
		return 0, fmt.Errorf("create run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, st := range steps {
		if _, err := tx.Exec(
			`INSERT INTO orchestration_run_steps(run_id,host_id,host_name,host_ip,seq,template_id,template_name,status)
			 VALUES(?,?,?,?,?,?,?,'pending')`,
			runID, st.HostID, st.HostName, st.HostIP, st.Seq, st.TemplateID, st.TemplateName); err != nil {
			return 0, err
		}
	}
	return runID, tx.Commit()
}

// RunStepRef 运行明细引用（含行 ID 供状态回写）。
type RunStepRef struct {
	model.OrchestrationRunStep
	RecID int64
}

// RunSteps 运行明细（按主机、步骤排序）。
func (r *OrchestrationRepo) RunSteps(runID int64) ([]RunStepRef, error) {
	rows, err := DB.Query(
		`SELECT id,run_id,host_id,host_name,host_ip,seq,template_id,template_name,status,attempt,output,error,started_at,finished_at
		 FROM orchestration_run_steps WHERE run_id=? ORDER BY host_id,seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RunStepRef{}
	for rows.Next() {
		var it RunStepRef
		if err := rows.Scan(&it.RecID, &it.RunID, &it.HostID, &it.HostName, &it.HostIP, &it.Seq,
			&it.TemplateID, &it.TemplateName, &it.Status, &it.Attempt, &it.Output, &it.Error,
			&it.StartedAt, &it.FinishedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// UpdateRunStepStatus 回写单步状态与输出。
func (r *OrchestrationRepo) UpdateRunStepStatus(recID int64, status string, attempt int, output, errMsg string) error {
	_, err := DB.Exec(
		`UPDATE orchestration_run_steps SET status=?, attempt=?, output=CASE WHEN ?<>'' THEN ? ELSE output END, error=?,
		 started_at=COALESCE(started_at, datetime('now','localtime')),
		 finished_at=CASE WHEN ? IN ('success','failed','skipped') THEN datetime('now','localtime') ELSE finished_at END
		 WHERE id=?`,
		status, attempt, output, output, errMsg, status, recID)
	return err
}

// SkipRemaining 将某主机尚未执行的后续步骤标记 skipped。
func (r *OrchestrationRepo) SkipRemaining(runID, hostID int64, afterSeq int) error {
	_, err := DB.Exec(
		`UPDATE orchestration_run_steps SET status='skipped',
		 finished_at=datetime('now','localtime')
		 WHERE run_id=? AND host_id=? AND seq>? AND status='pending'`, runID, hostID, afterSeq)
	return err
}

// FinishRun 汇总主机成败并落终态。一台主机存在 failed 步骤即失败。
func (r *OrchestrationRepo) FinishRun(runID int64) (string, error) {
	var failN, okN sql.NullInt64
	err := DB.QueryRow(`
		SELECT
		  SUM(CASE WHEN cnt>0 THEN 1 ELSE 0 END),
		  SUM(CASE WHEN cnt=0 THEN 1 ELSE 0 END)
		FROM (
		  SELECT host_id, SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END) AS cnt
		  FROM orchestration_run_steps WHERE run_id=? GROUP BY host_id
		)`, runID).Scan(&failN, &okN)
	if err != nil {
		return "", err
	}
	fails, oks := int(failN.Int64), int(okN.Int64)

	status := "failed"
	switch {
	case fails == 0 && oks > 0:
		status = "success"
	case fails > 0 && oks > 0:
		status = "partial"
	}
	_, uerr := DB.Exec(
		`UPDATE orchestration_runs SET status=?, ok_hosts=?, fail_hosts=?, finished_at=datetime('now','localtime') WHERE id=?`,
		status, oks, fails, runID)
	return status, uerr
}

// ListRuns 分页运行历史。
func (r *OrchestrationRepo) ListRuns(page, pageSize int) ([]model.OrchestrationRun, int64, error) {
	var total int64
	if err := DB.QueryRow(`SELECT COUNT(*) FROM orchestration_runs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := DB.Query(
		`SELECT id,orchestration_id,name,exec_mode,status,total_hosts,ok_hosts,fail_hosts,trigger_type,created_at,finished_at
		 FROM orchestration_runs ORDER BY id DESC LIMIT ? OFFSET ?`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.OrchestrationRun{}
	for rows.Next() {
		var it model.OrchestrationRun
		if err := rows.Scan(&it.ID, &it.OrchestrationID, &it.Name, &it.ExecMode, &it.Status,
			&it.TotalHosts, &it.OkHosts, &it.FailHosts, &it.TriggerType, &it.CreatedAt, &it.FinishedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, it)
	}
	return items, total, rows.Err()
}

// GetRun 单条运行记录。
func (r *OrchestrationRepo) GetRun(id int64) (*model.OrchestrationRun, error) {
	it := &model.OrchestrationRun{}
	err := DB.QueryRow(
		`SELECT id,orchestration_id,name,exec_mode,status,total_hosts,ok_hosts,fail_hosts,trigger_type,created_at,finished_at
		 FROM orchestration_runs WHERE id=?`, id).
		Scan(&it.ID, &it.OrchestrationID, &it.Name, &it.ExecMode, &it.Status,
			&it.TotalHosts, &it.OkHosts, &it.FailHosts, &it.TriggerType, &it.CreatedAt, &it.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return it, nil
}

// CleanupRunsBefore 清理超期已结束运行（级联明细）。
func (r *OrchestrationRepo) CleanupRunsBefore(days int) (int64, error) {
	res, err := DB.Exec(
		`DELETE FROM orchestration_runs
		 WHERE finished_at IS NOT NULL AND finished_at < datetime('now','localtime', ?)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return n, err
}
