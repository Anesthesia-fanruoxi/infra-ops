package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// StackRepo 套件运行记录存取。
type StackRepo struct{}

func NewStackRepo() *StackRepo { return &StackRepo{} }

const stackRunCols = `id,instance_id,op,stack_key,stack_name,mode,status,total,success_cnt,fail_cnt,params_json,created_at,finished_at`

func scanStackRun(sc interface{ Scan(...interface{}) error }, r *model.StackRun) error {
	var finished sql.NullString
	if err := sc.Scan(&r.ID, &r.InstanceID, &r.Op, &r.StackKey, &r.StackName, &r.Mode, &r.Status, &r.Total,
		&r.SuccessCnt, &r.FailCnt, &r.ParamsJSON, &r.CreatedAt, &finished); err != nil {
		return err
	}
	if finished.Valid {
		r.FinishedAt = &finished.String
	}
	return nil
}

// CreateRun 事务创建运行与主机行。
func (r *StackRepo) CreateRun(run *model.StackRun, hosts []model.StackRunHost) (int64, error) {
	tx, err := store.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	op := strings.TrimSpace(run.Op)
	if op == "" {
		op = "create"
	}
	res, err := tx.Exec(
		`INSERT INTO stack_runs(instance_id,op,stack_key,stack_name,mode,status,total,params_json) VALUES(?,?,?,?,?,'running',?,?)`,
		run.InstanceID, op, run.StackKey, run.StackName, run.Mode, len(hosts), run.ParamsJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("create stack run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, h := range hosts {
		if _, err := tx.Exec(
			`INSERT INTO stack_run_hosts(run_id,host_id,host_name,host_ip,role,seq,params_json,status,prereq_status,node_status,bootstrap_status)
			 VALUES(?,?,?,?,?,?,?,'pending',?,?,?)`,
			runID, h.HostID, h.HostName, h.HostIP, h.Role, h.Seq, h.ParamsJSON,
			h.PrereqStatus, h.NodeStatus, h.BootstrapStatus,
		); err != nil {
			return 0, fmt.Errorf("create stack run host: %w", err)
		}
	}
	return runID, tx.Commit()
}

// GetRun 运行头。
func (r *StackRepo) GetRun(id int64) (*model.StackRun, error) {
	run := &model.StackRun{}
	err := scanStackRun(store.DB.QueryRow(`SELECT `+stackRunCols+` FROM stack_runs WHERE id=?`, id), run)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return run, nil
}

// RunHosts 运行下全部主机，按 seq。
func (r *StackRepo) RunHosts(runID int64) ([]model.StackRunHost, error) {
	rows, err := store.DB.Query(
		`SELECT id,run_id,host_id,host_name,host_ip,role,seq,params_json,status,
			prereq_status,node_status,bootstrap_status,output,error,started_at,finished_at
		 FROM stack_run_hosts WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.StackRunHost{}
	for rows.Next() {
		var h model.StackRunHost
		var started, finished sql.NullString
		if err := rows.Scan(&h.ID, &h.RunID, &h.HostID, &h.HostName, &h.HostIP, &h.Role, &h.Seq,
			&h.ParamsJSON, &h.Status, &h.PrereqStatus, &h.NodeStatus, &h.BootstrapStatus,
			&h.Output, &h.Error, &started, &finished); err != nil {
			return nil, err
		}
		if started.Valid {
			h.StartedAt = &started.String
		}
		if finished.Valid {
			h.FinishedAt = &finished.String
		}
		items = append(items, h)
	}
	return items, rows.Err()
}

// UpdateHost 回写一台主机的阶段/总体状态。
func (r *StackRepo) UpdateHost(h *model.StackRunHost) error {
	_, err := store.DB.Exec(
		`UPDATE stack_run_hosts SET status=?, prereq_status=?, node_status=?, bootstrap_status=?,
			output=?, error=?,
			started_at=COALESCE(started_at, datetime('now','localtime')),
			finished_at=CASE WHEN ? IN ('success','failed','skipped') THEN datetime('now','localtime') ELSE finished_at END
		 WHERE id=?`,
		h.Status, h.PrereqStatus, h.NodeStatus, h.BootstrapStatus, h.Output, h.Error, h.Status, h.ID,
	)
	return err
}

// FinishRun 汇总成败并落终态。
func (r *StackRepo) FinishRun(runID int64) (string, error) {
	var successCnt, failCnt, total int
	if err := store.DB.QueryRow(
		`SELECT
			SUM(CASE WHEN status='success' THEN 1 ELSE 0 END),
			SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),
			COUNT(*)
		 FROM stack_run_hosts WHERE run_id=?`, runID,
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
	_, err := store.DB.Exec(
		`UPDATE stack_runs SET status=?, success_cnt=?, fail_cnt=?, finished_at=datetime('now','localtime') WHERE id=?`,
		status, successCnt, failCnt, runID,
	)
	return status, err
}

// ListRuns 分页运行列表。
func (r *StackRepo) ListRuns(page, pageSize int) ([]model.StackRun, int64, error) {
	var total int64
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM stack_runs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := store.DB.Query(
		`SELECT `+stackRunCols+` FROM stack_runs ORDER BY id DESC LIMIT ? OFFSET ?`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.StackRun{}
	for rows.Next() {
		var run model.StackRun
		if err := scanStackRun(rows, &run); err != nil {
			return nil, 0, err
		}
		items = append(items, run)
	}
	return items, total, rows.Err()
}

// ListRunningRuns 当前 running 的套件运行。
func (r *StackRepo) ListRunningRuns() ([]model.StackRun, error) {
	rows, err := store.DB.Query(`SELECT ` + stackRunCols + ` FROM stack_runs WHERE status='running' ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.StackRun{}
	for rows.Next() {
		var run model.StackRun
		if err := scanStackRun(rows, &run); err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

// AppendLogs 批量写日志，返回落库后的行。
func (r *StackRepo) AppendLogs(runID int64, rows []model.StackRunLog) ([]model.StackRunLog, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	tx, err := store.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := ""
	_ = tx.QueryRow(`SELECT datetime('now','localtime')`).Scan(&now)
	out := make([]model.StackRunLog, 0, len(rows))
	for _, l := range rows {
		res, err := tx.Exec(
			`INSERT INTO stack_run_logs(run_id,phase,host_id,host_ip,text,created_at) VALUES(?,?,?,?,?,?)`,
			runID, l.Phase, l.HostID, l.HostIP, l.Text, now)
		if err != nil {
			return nil, fmt.Errorf("append stack log: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		l.ID = id
		l.RunID = runID
		l.CreatedAt = now
		out = append(out, l)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// RunLogs 最近 2000 行日志。
func (r *StackRepo) RunLogs(runID int64) ([]model.StackRunLog, error) {
	rows, err := store.DB.Query(
		`SELECT id,run_id,phase,host_id,host_ip,text,created_at
		 FROM (
		   SELECT id,run_id,phase,host_id,host_ip,text,created_at
		   FROM stack_run_logs WHERE run_id=?
		   ORDER BY id DESC LIMIT 2000
		 ) t ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.StackRunLog{}
	for rows.Next() {
		var l model.StackRunLog
		if err := rows.Scan(&l.ID, &l.RunID, &l.Phase, &l.HostID, &l.HostIP, &l.Text, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}

const stackInstCols = `id,name,stack_key,stack_name,mode,category,status,params_json,components_json,created_at,updated_at`

func scanStackInstance(sc interface{ Scan(...interface{}) error }, inst *model.StackInstance) error {
	return sc.Scan(&inst.ID, &inst.Name, &inst.StackKey, &inst.StackName, &inst.Mode, &inst.Category,
		&inst.Status, &inst.ParamsJSON, &inst.ComponentsJSON, &inst.CreatedAt, &inst.UpdatedAt)
}

// CreateInstance 新建集群实例（部署开始即落库，便于失败后仍可查看/删除）。
func (r *StackRepo) CreateInstance(inst *model.StackInstance) (int64, error) {
	comps := strings.TrimSpace(inst.ComponentsJSON)
	if comps == "" {
		comps = "[]"
	}
	params := strings.TrimSpace(inst.ParamsJSON)
	if params == "" {
		params = "{}"
	}
	status := strings.TrimSpace(inst.Status)
	if status == "" {
		status = "deploying"
	}
	res, err := store.DB.Exec(
		`INSERT INTO stack_instances(name,stack_key,stack_name,mode,category,status,params_json,components_json)
		 VALUES(?,?,?,?,?,?,?,?)`,
		inst.Name, inst.StackKey, inst.StackName, inst.Mode, inst.Category, status, params, comps,
	)
	if err != nil {
		return 0, fmt.Errorf("create stack instance: %w", err)
	}
	return res.LastInsertId()
}

// GetInstance 实例头（不含主机）。
func (r *StackRepo) GetInstance(id int64) (*model.StackInstance, error) {
	inst := &model.StackInstance{}
	err := scanStackInstance(store.DB.QueryRow(`SELECT `+stackInstCols+` FROM stack_instances WHERE id=?`, id), inst)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return inst, nil
}

// InstanceHosts 实例成员；activeOnly 为真时只返回未移除的。
func (r *StackRepo) InstanceHosts(instanceID int64, activeOnly bool) ([]model.StackInstanceHost, error) {
	q := `SELECT id,instance_id,host_id,host_name,host_ip,role,seq,params_json,components_json,status
		  FROM stack_instance_hosts WHERE instance_id=?`
	if activeOnly {
		q += ` AND status='active'`
	}
	q += ` ORDER BY seq, id`
	rows, err := store.DB.Query(q, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.StackInstanceHost{}
	for rows.Next() {
		var h model.StackInstanceHost
		if err := rows.Scan(&h.ID, &h.InstanceID, &h.HostID, &h.HostName, &h.HostIP, &h.Role, &h.Seq,
			&h.ParamsJSON, &h.ComponentsJSON, &h.Status); err != nil {
			return nil, err
		}
		items = append(items, h)
	}
	return items, rows.Err()
}

// GetInstanceFull 实例 + 全部成员。
func (r *StackRepo) GetInstanceFull(id int64) (*model.StackInstance, error) {
	inst, err := r.GetInstance(id)
	if err != nil || inst == nil {
		return inst, err
	}
	hosts, err := r.InstanceHosts(id, false)
	if err != nil {
		return nil, err
	}
	inst.Hosts = hosts
	n := 0
	for _, h := range hosts {
		if h.Status == "active" {
			n++
		}
	}
	inst.HostCount = n
	return inst, nil
}

// ListInstances 分页实例列表（含活跃主机数）。
func (r *StackRepo) ListInstances(page, pageSize int) ([]model.StackInstance, int64, error) {
	var total int64
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM stack_instances`).Scan(&total); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	rows, err := store.DB.Query(
		`SELECT i.id,i.name,i.stack_key,i.stack_name,i.mode,i.category,i.status,i.params_json,i.components_json,
			i.created_at,i.updated_at,
			(SELECT COUNT(*) FROM stack_instance_hosts h WHERE h.instance_id=i.id AND h.status='active') AS host_count
		 FROM stack_instances i ORDER BY i.id DESC LIMIT ? OFFSET ?`, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.StackInstance{}
	for rows.Next() {
		var inst model.StackInstance
		if err := rows.Scan(&inst.ID, &inst.Name, &inst.StackKey, &inst.StackName, &inst.Mode, &inst.Category,
			&inst.Status, &inst.ParamsJSON, &inst.ComponentsJSON, &inst.CreatedAt, &inst.UpdatedAt, &inst.HostCount); err != nil {
			return nil, 0, err
		}
		items = append(items, inst)
	}
	return items, total, rows.Err()
}

// UpdateInstance 改名称 / 状态 / 参数 / 组件。空字段表示不改。
func (r *StackRepo) UpdateInstance(id int64, name, status, paramsJSON, componentsJSON string) error {
	_, err := store.DB.Exec(
		`UPDATE stack_instances SET
			name=CASE WHEN ?!='' THEN ? ELSE name END,
			status=CASE WHEN ?!='' THEN ? ELSE status END,
			params_json=CASE WHEN ?!='' THEN ? ELSE params_json END,
			components_json=CASE WHEN ?!='' THEN ? ELSE components_json END,
			updated_at=datetime('now','localtime')
		 WHERE id=?`,
		name, name, status, status, paramsJSON, paramsJSON, componentsJSON, componentsJSON, id,
	)
	return err
}

// DeleteInstance 删除实例及其成员（流程记录保留，instance_id 仍可追溯）。
func (r *StackRepo) DeleteInstance(id int64) error {
	tx, err := store.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM stack_instance_hosts WHERE instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM stack_instances WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertInstanceHost 写入或恢复成员。
func (r *StackRepo) UpsertInstanceHost(h *model.StackInstanceHost) error {
	comps := strings.TrimSpace(h.ComponentsJSON)
	if comps == "" {
		comps = "[]"
	}
	params := strings.TrimSpace(h.ParamsJSON)
	if params == "" {
		params = "{}"
	}
	status := strings.TrimSpace(h.Status)
	if status == "" {
		status = "active"
	}
	_, err := store.DB.Exec(
		`INSERT INTO stack_instance_hosts(instance_id,host_id,host_name,host_ip,role,seq,params_json,components_json,status)
		 VALUES(?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(instance_id, host_id) DO UPDATE SET
			host_name=excluded.host_name, host_ip=excluded.host_ip, role=excluded.role, seq=excluded.seq,
			params_json=excluded.params_json, components_json=excluded.components_json, status=excluded.status`,
		h.InstanceID, h.HostID, h.HostName, h.HostIP, h.Role, h.Seq, params, comps, status,
	)
	return err
}

// MarkInstanceHostRemoved 缩容成功后标记移除。
func (r *StackRepo) MarkInstanceHostRemoved(instanceID, hostID int64) error {
	_, err := store.DB.Exec(
		`UPDATE stack_instance_hosts SET status='removed' WHERE instance_id=? AND host_id=?`,
		instanceID, hostID,
	)
	return err
}

// InstanceHasRunning 该实例是否有进行中的流程。
func (r *StackRepo) InstanceHasRunning(instanceID int64) (bool, error) {
	var n int
	err := store.DB.QueryRow(`SELECT COUNT(*) FROM stack_runs WHERE instance_id=? AND status='running'`, instanceID).Scan(&n)
	return n > 0, err
}

// ListRunsByInstance 某实例下的流程，新的在前。
func (r *StackRepo) ListRunsByInstance(instanceID int64) ([]model.StackRun, error) {
	rows, err := store.DB.Query(`SELECT `+stackRunCols+` FROM stack_runs WHERE instance_id=? ORDER BY id DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.StackRun{}
	for rows.Next() {
		var run model.StackRun
		if err := scanStackRun(rows, &run); err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}
