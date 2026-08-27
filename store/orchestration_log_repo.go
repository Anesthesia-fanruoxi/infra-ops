package store

import (
	"fmt"

	"infra-ops/model"
)

// OrchestrationLogRepo 运行日志存取。
type OrchestrationLogRepo struct{}

func NewOrchestrationLogRepo() *OrchestrationLogRepo { return &OrchestrationLogRepo{} }

// AppendRunLogs 批量写入日志行（单事务）。返回落库后的行（填充自增 id 与 created_at）。
func (r *OrchestrationLogRepo) AppendRunLogs(runID int64, rows []model.OrchestrationRunLog) ([]model.OrchestrationRunLog, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	tx, err := DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := ""
	_ = tx.QueryRow(`SELECT datetime('now','localtime')`).Scan(&now)
	out := make([]model.OrchestrationRunLog, 0, len(rows))
	for _, l := range rows {
		res, err := tx.Exec(
			`INSERT INTO orchestration_run_logs(run_id,seq,host_id,host_ip,text,created_at) VALUES(?,?,?,?,?,?)`,
			runID, l.Seq, l.HostID, l.HostIP, l.Text, now)
		if err != nil {
			return nil, fmt.Errorf("append log: %w", err)
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

// RunStepLogs 某步骤已落库日志（id 升序，最近 2000 行封顶）。
func (r *OrchestrationLogRepo) RunStepLogs(runID int64, seq int) ([]model.OrchestrationRunLog, error) {
	rows, err := DB.Query(
		`SELECT id,run_id,seq,host_id,host_ip,text,created_at
		 FROM (
		   SELECT id,run_id,seq,host_id,host_ip,text,created_at
		   FROM orchestration_run_logs
		   WHERE run_id=? AND seq=?
		   ORDER BY id DESC LIMIT 2000
		 ) t ORDER BY id ASC`, runID, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.OrchestrationRunLog{}
	for rows.Next() {
		var l model.OrchestrationRunLog
		if err := rows.Scan(&l.ID, &l.RunID, &l.Seq, &l.HostID, &l.HostIP, &l.Text, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}
