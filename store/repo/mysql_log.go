package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// MySQLLogRepo MySQL 工具的使用记录：只记 SQL 执行记录（mysql_sql_logs）——
// 人在工具里执行 SQL 的记录，不区分语句从哪来，是否借鉴了 AI 生成的结果也不作为维度。
//
// AI 调用记录已收敛到统一表（ai_logs，见 repo.AILogRepo）：MySQL 的 AI 记录以
// menu=mysql 写入，读取走统一入口（/api/ai/logs?menu=mysql）。
type MySQLLogRepo struct{}

func NewMySQLLogRepo() *MySQLLogRepo { return &MySQLLogRepo{} }

// ---------------- 写入 ----------------

// AddSQLLog 落一条 SQL 执行记录。
func (r *MySQLLogRepo) AddSQLLog(e *model.MySQLSQLLog) (int64, error) {
	res, err := store.DB.Exec(
		`INSERT INTO mysql_sql_logs(conn_id, conn_name, schema_name, sql_text, statement_count,
		   sql_type, statement_kind, mode, confirmed, row_count, affected_rows, elapsed_ms,
		   status, error)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ConnID, e.ConnName, e.SchemaName, e.SQL, e.StatementCount,
		e.SQLType, e.StatementKind, e.Mode, boolInt(e.Confirmed), e.RowCount, e.AffectedRows, e.ElapsedMs,
		e.Status, e.Error)
	if err != nil {
		return 0, fmt.Errorf("mysql sql log insert: %w", err)
	}
	return res.LastInsertId()
}

// ---------------- 查询 ----------------

const sqlLogColumns = `s.id, s.conn_id, s.conn_name, s.schema_name, s.sql_text, s.statement_count,
	s.sql_type, s.statement_kind, s.mode, s.confirmed, s.row_count, s.affected_rows,
	s.elapsed_ms, s.status, s.error, s.created_at`

// logWhere 拼过滤条件（连接 / 状态）；无条件时返回恒真式，调用方无需分支。
func logWhere(f model.MySQLLogFilter, alias string) (string, []interface{}) {
	conds := []string{}
	args := []interface{}{}
	if f.ConnID > 0 {
		conds = append(conds, alias+"conn_id = ?")
		args = append(args, f.ConnID)
	}
	if f.Status != "" {
		conds = append(conds, alias+"status = ?")
		args = append(args, f.Status)
	}
	if len(conds) == 0 {
		return "1=1", args
	}
	return strings.Join(conds, " AND "), args
}

// withPage 复制一份参数再追加分页，避免 append 复用底层数组把分页值串进上一次查询。
func withPage(args []interface{}, pageSize, offset int) []interface{} {
	out := make([]interface{}, 0, len(args)+2)
	out = append(out, args...)
	return append(out, pageSize, offset)
}

// ListSQLLogs 分页取 SQL 执行记录（新 → 旧）。
func (r *MySQLLogRepo) ListSQLLogs(f model.MySQLLogFilter) ([]model.MySQLSQLLog, int, error) {
	where, args := logWhere(f, "s.")
	if f.SQLType != "" {
		where += " AND s.sql_type = ?"
		args = append(args, f.SQLType)
	}

	var total int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM mysql_sql_logs s WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("mysql sql log count: %w", err)
	}

	q := `SELECT ` + sqlLogColumns + ` FROM mysql_sql_logs s WHERE ` + where +
		` ORDER BY s.id DESC LIMIT ? OFFSET ?`
	rows, err := store.DB.Query(q, withPage(args, f.PageSize, f.Offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("mysql sql log list: %w", err)
	}
	defer rows.Close()

	out := []model.MySQLSQLLog{}
	for rows.Next() {
		var e model.MySQLSQLLog
		var confirmed int
		if err := rows.Scan(&e.ID, &e.ConnID, &e.ConnName, &e.SchemaName, &e.SQL, &e.StatementCount,
			&e.SQLType, &e.StatementKind, &e.Mode, &confirmed, &e.RowCount, &e.AffectedRows,
			&e.ElapsedMs, &e.Status, &e.Error, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("mysql sql log scan: %w", err)
		}
		e.Confirmed = confirmed != 0
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// SummarizeSQLLogs 汇总 SQL 执行记录（全量，不受分页影响）。
func (r *MySQLLogRepo) SummarizeSQLLogs(f model.MySQLLogFilter) (*model.MySQLSQLLogSummary, error) {
	where, args := logWhere(f, "")
	if f.SQLType != "" {
		where += " AND sql_type = ?"
		args = append(args, f.SQLType)
	}
	sum := &model.MySQLSQLLogSummary{ByType: []model.MySQLSQLTypeStat{}}
	var avg sql.NullFloat64
	err := store.DB.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(status = ?), 0), COALESCE(SUM(status = ?), 0),
			COALESCE(SUM(sql_type IN (?, ?)), 0), COALESCE(SUM(sql_type IN (?, ?, ?)), 0),
			COALESCE(SUM(row_count), 0), COALESCE(SUM(affected_rows), 0), AVG(elapsed_ms),
			COALESCE(MIN(created_at), ''), COALESCE(MAX(created_at), '')
		FROM mysql_sql_logs WHERE `+where,
		append([]interface{}{
			model.LogStatusOK, model.LogStatusError,
			model.SQLTypeDQL, model.SQLTypeSession,
			model.SQLTypeDML, model.SQLTypeDDL, model.SQLTypeOther,
		}, args...)...,
	).Scan(&sum.Runs, &sum.OK, &sum.Failed, &sum.QueryRuns, &sum.WriteRuns,
		&sum.RowsReturned, &sum.RowsAffected, &avg, &sum.FirstAt, &sum.LastAt)
	if err != nil {
		return nil, fmt.Errorf("mysql sql log summary: %w", err)
	}
	if avg.Valid {
		sum.AvgElapsedMs = int64(avg.Float64)
	}

	rows, err := store.DB.Query(`SELECT sql_type, COUNT(*) FROM mysql_sql_logs
		WHERE `+where+` GROUP BY sql_type ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql sql log type stat: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t model.MySQLSQLTypeStat
		if err := rows.Scan(&t.SQLType, &t.Runs); err != nil {
			return nil, fmt.Errorf("mysql sql log type scan: %w", err)
		}
		sum.ByType = append(sum.ByType, t)
	}
	return sum, rows.Err()
}

// ---------------- 清理 ----------------

// ClearLogs 清空 SQL 执行记录；connID 为 0 表示不限连接。
func (r *MySQLLogRepo) ClearLogs(connID int64) (int64, error) {
	q := `DELETE FROM mysql_sql_logs`
	args := []interface{}{}
	if connID > 0 {
		q += ` WHERE conn_id = ?`
		args = append(args, connID)
	}
	res, err := store.DB.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("mysql sql log clear: %w", err)
	}
	return res.RowsAffected()
}

// PurgeBefore 删除保留期之前的 SQL 执行记录，返回清理条数；days <= 0 时不清理。
// 与部署历史的保留策略同源（setting.SettingLogRetentionDays，默认 30 天）。
func (r *MySQLLogRepo) PurgeBefore(days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res, err := store.DB.Exec(
		`DELETE FROM mysql_sql_logs WHERE created_at < datetime('now','localtime', ?)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, fmt.Errorf("mysql sql log purge: %w", err)
	}
	return res.RowsAffected()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
