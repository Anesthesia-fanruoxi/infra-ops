// ai_log.go 统一 AI 调用记录（ai_logs）的数据存取：各工具写入，读取侧按
// 菜单（页面 id）+ 连接过滤。与 SQL 执行记录（mysql_sql_logs）无关，各记各的。
package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// AILogRepo 统一 AI 调用记录的数据存取。
type AILogRepo struct{}

func NewAILogRepo() *AILogRepo { return &AILogRepo{} }

// Add 落一条 AI 调用记录，返回记录 id（旁路信息，调用方对错误吞掉处理）。
func (r *AILogRepo) Add(e *model.AILog) (int64, error) {
	res, err := store.DB.Exec(
		`INSERT INTO ai_logs(menu, conn_id, conn_name, schema_name, kind, mode, model, base_url,
		   batch_no, batch_total, table_count, metric_count, question, expr, content,
		   prompt_tokens, completion_tokens, total_tokens, token_source, elapsed_ms, status, error)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.Menu, e.ConnID, e.ConnName, e.SchemaName, e.Kind, e.Mode, e.Model, e.BaseURL,
		e.BatchNo, e.BatchTotal, e.TableCount, e.MetricCount, e.Question, e.Expr, e.Content,
		e.PromptTokens, e.CompletionTokens, e.TotalTokens, e.TokenSource, e.ElapsedMs, e.Status, e.Error)
	if err != nil {
		return 0, fmt.Errorf("ai log insert: %w", err)
	}
	return res.LastInsertId()
}

const aiLogColumns = `l.id, l.menu, l.conn_id, l.conn_name, l.schema_name, l.kind, l.mode, l.model, l.base_url,
	l.batch_no, l.batch_total, l.table_count, l.metric_count, l.question, l.expr, l.content,
	l.prompt_tokens, l.completion_tokens, l.total_tokens, l.token_source, l.elapsed_ms,
	l.status, l.error, l.created_at`

// aiLogWhere 拼过滤条件；无条件时返回恒真式，调用方无需分支。
func aiLogWhere(f model.AILogFilter) (string, []interface{}) {
	conds := []string{}
	args := []interface{}{}
	if f.Menu != "" {
		conds = append(conds, "menu = ?")
		args = append(args, f.Menu)
	}
	if f.ConnID > 0 {
		conds = append(conds, "conn_id = ?")
		args = append(args, f.ConnID)
	}
	if f.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if len(conds) == 0 {
		return "1=1", args
	}
	return strings.Join(conds, " AND "), args
}

// List 分页取 AI 调用记录（新 → 旧）。
func (r *AILogRepo) List(f model.AILogFilter) ([]model.AILog, int, error) {
	where, args := aiLogWhere(f)

	var total int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM ai_logs l WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("ai log count: %w", err)
	}

	q := `SELECT ` + aiLogColumns + ` FROM ai_logs l WHERE ` + where +
		` ORDER BY l.id DESC LIMIT ? OFFSET ?`
	rows, err := store.DB.Query(q, withPage(args, f.PageSize, f.Offset())...)
	if err != nil {
		return nil, 0, fmt.Errorf("ai log list: %w", err)
	}
	defer rows.Close()

	out := []model.AILog{}
	for rows.Next() {
		var e model.AILog
		if err := rows.Scan(&e.ID, &e.Menu, &e.ConnID, &e.ConnName, &e.SchemaName, &e.Kind, &e.Mode, &e.Model, &e.BaseURL,
			&e.BatchNo, &e.BatchTotal, &e.TableCount, &e.MetricCount, &e.Question, &e.Expr, &e.Content,
			&e.PromptTokens, &e.CompletionTokens, &e.TotalTokens, &e.TokenSource, &e.ElapsedMs,
			&e.Status, &e.Error, &e.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("ai log scan: %w", err)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// Summarize 汇总 AI 调用记录（全量，不受分页影响）。
func (r *AILogRepo) Summarize(f model.AILogFilter) (*model.AILogSummary, error) {
	where, args := aiLogWhere(f)
	sum := &model.AILogSummary{ByKind: []model.AILogKindStat{}, ByMenu: []model.AILogMenuStat{}}
	var avg sql.NullFloat64
	err := store.DB.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(status = ?), 0), COALESCE(SUM(status = ?), 0),
			COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(token_source = ?), 0), AVG(elapsed_ms),
			COALESCE(MIN(created_at), ''), COALESCE(MAX(created_at), '')
		FROM ai_logs WHERE `+where,
		append([]interface{}{model.LogStatusOK, model.LogStatusError, model.TokenSourceEstimated}, args...)...,
	).Scan(&sum.Calls, &sum.OK, &sum.Failed,
		&sum.PromptTokens, &sum.CompletionTokens, &sum.TotalTokens, &sum.EstimatedCalls,
		&avg, &sum.FirstAt, &sum.LastAt)
	if err != nil {
		return nil, fmt.Errorf("ai log summary: %w", err)
	}
	if avg.Valid {
		sum.AvgElapsedMs = int64(avg.Float64)
	}

	rows, err := store.DB.Query(`SELECT kind, COUNT(*), COALESCE(SUM(total_tokens), 0)
		FROM ai_logs WHERE `+where+` GROUP BY kind ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("ai log kind stat: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k model.AILogKindStat
		if err := rows.Scan(&k.Kind, &k.Calls, &k.TotalTokens); err != nil {
			return nil, fmt.Errorf("ai log kind scan: %w", err)
		}
		sum.ByKind = append(sum.ByKind, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	mrows, err := store.DB.Query(`SELECT menu, COUNT(*), COALESCE(SUM(total_tokens), 0)
		FROM ai_logs WHERE `+where+` GROUP BY menu ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("ai log menu stat: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var m model.AILogMenuStat
		if err := mrows.Scan(&m.Menu, &m.Calls, &m.TotalTokens); err != nil {
			return nil, fmt.Errorf("ai log menu scan: %w", err)
		}
		sum.ByMenu = append(sum.ByMenu, m)
	}
	return sum, mrows.Err()
}

// Clear 清空记录：menu 为空表示清全部菜单；connID 为 0 表示不限连接。
func (r *AILogRepo) Clear(menu string, connID int64) (int64, error) {
	conds := []string{}
	args := []interface{}{}
	if menu != "" {
		conds = append(conds, "menu = ?")
		args = append(args, menu)
	}
	if connID > 0 {
		conds = append(conds, "conn_id = ?")
		args = append(args, connID)
	}
	q := "DELETE FROM ai_logs"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	res, err := store.DB.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("ai log clear: %w", err)
	}
	return res.RowsAffected()
}

// PurgeBefore 删除保留期之前的记录，返回清理条数；days <= 0 时不清理。
// 与部署历史的保留策略同源（setting.SettingLogRetentionDays，默认 30 天）。
func (r *AILogRepo) PurgeBefore(days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res, err := store.DB.Exec(
		`DELETE FROM ai_logs WHERE created_at < datetime('now','localtime', ?)`,
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return 0, fmt.Errorf("ai log purge: %w", err)
	}
	return res.RowsAffected()
}
