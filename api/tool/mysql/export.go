package mysql

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
)

// 查询导出：把一条 SELECT 的结果流式写成本地文件。
//
// 为什么不走 HTTP 响应体把文件吐回前端：桌面端的资产服务器会把整个响应缓冲在
// 内存里（wailsapp/wails#2847 同类限制，desktop/file_service.go 里已有同样的结论），
// 几百万行的导出必然把窗口拖垮。所以路径由前端弹原生保存对话框拿到，服务端边查边写。

const (
	exportFormatSQL  = "sql"
	exportFormatXLSX = "xlsx"
	exportFormatCSV  = "csv"

	exportFlushRows = 200 // 每多少行落一次盘并推一次进度

	// 兜底上限：本地直连不给 SQL 设超时，但也不能让一个任务无限挂着。
	maxExportTimeout = 2 * time.Hour
)

type exportReq struct {
	SQL         string `json:"sql" binding:"required"`
	Schema      string `json:"schema"`
	Format      string `json:"format"`
	Target      string `json:"target"` // 本地绝对路径
	Table       string `json:"table"`  // INSERT 的目标表；留空则从 SQL 推断
	Sheet       string `json:"sheet"`  // xlsx 工作表名；留空取表名
	BatchRows   int    `json:"batch_rows"`
	BatchBytes  int    `json:"batch_bytes"`
	ProgressKey string `json:"progress_key"`
}

type exportResult struct {
	Target    string `json:"target"`
	Format    string `json:"format"`
	Rows      int64  `json:"rows"`
	Bytes     int64  `json:"bytes"`
	Batches   int64  `json:"batches"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

type exportJob struct {
	conn       *model.MySQLConn
	sql        string
	schema     string
	target     string
	format     string
	table      string // 已限定的 INSERT 目标表（含反引号）
	sheet      string
	batchRows  int
	batchBytes int
	prog       *transferHandle
}

// Export POST /api/mysql/:id/export
func (h *Handler) Export(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req exportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = exportFormatSQL
	}
	switch format {
	case exportFormatSQL, exportFormatXLSX, exportFormatCSV:
	default:
		resp.Fail(c, resp.CodeBadRequest, "导出格式只能是 sql / xlsx / csv")
		return
	}

	schema := strings.TrimSpace(req.Schema)
	target, err := normalizeExportTarget(req.Target, format)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.SQL) == "" {
		resp.Fail(c, resp.CodeBadRequest, "SQL 不能为空")
		return
	}

	stmts, an := analyzeSQL(req.SQL)
	// 导出是纯读通道：只放行单条查询语句，免得「导出」变成执行写语句的后门
	if an.Type != model.SQLTypeDQL || len(stmts) != 1 {
		resp.Fail(c, resp.CodeBadRequest, "导出只支持单条查询语句，当前是 "+an.Type)
		return
	}

	job := &exportJob{
		conn: conn, sql: stmts[0], schema: schema, target: target, format: format,
		sheet: strings.TrimSpace(req.Sheet), batchRows: req.BatchRows, batchBytes: req.BatchBytes,
	}
	table, plain := qualifyTable(schema, req.Table, stmts[0])
	if format == exportFormatSQL {
		if table == "" {
			resp.Fail(c, resp.CodeBadRequest, "无法从 SQL 推断目标表名，请手动填写表名")
			return
		}
		job.table = table
	}
	if job.sheet == "" {
		job.sheet = plain
	}

	started := time.Now()
	job.prog = h.transfers.begin(req.ProgressKey, "export")
	ctx, cancel := context.WithTimeout(c.Request.Context(), maxExportTimeout)
	defer cancel()

	res, err := h.runExport(ctx, job)
	res.ElapsedMs = time.Since(started).Milliseconds()
	detail := transferLogDetail("导出为 "+format, target, req.SQL)
	if err != nil {
		job.prog.Finish(err)
		h.recordTransferLog(conn, schema, model.SQLTypeDQL, "EXPORT", detail, 1, res.Rows, 0, time.Since(started), err)
		resp.Fail(c, resp.CodeInternal, "导出失败: "+err.Error())
		return
	}
	res.Format = format
	job.prog.Running(res.Rows, 0, res.Batches, res.Bytes)
	job.prog.Finish(nil)
	h.recordTransferLog(conn, schema, model.SQLTypeDQL, "EXPORT", detail, 1, res.Rows, 0, time.Since(started), nil)
	resp.OK(c, res)
}

func (h *Handler) runExport(ctx context.Context, job *exportJob) (exportResult, error) {
	res := exportResult{Target: job.target}

	sess, err := h.openForSchema(job.conn, job.schema)
	if err != nil {
		return res, err
	}
	defer sess.Close()

	rows, err := sess.db.QueryContext(ctx, job.sql)
	if err != nil {
		return res, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return res, err
	}
	if len(cols) == 0 {
		return res, errors.New("这条语句没有返回结果集，无法导出")
	}
	types := columnTypes(rows, len(cols))

	f, err := os.Create(job.target)
	if err != nil {
		return res, fmt.Errorf("创建文件失败: %w", err)
	}
	cw := &countingWriter{w: f}
	bw := bufio.NewWriterSize(cw, 256*1024)

	fail := func(err error) (exportResult, error) {
		// 半成品文件与成功导出长得一模一样，留着会让人误以为导完了，直接删掉
		_ = f.Close()
		_ = os.Remove(job.target)
		return res, err
	}

	tw, err := newTableWriter(bw, job, cols, types)
	if err != nil {
		return fail(err)
	}
	if pw, ok := tw.(preambleWriter); ok {
		if err := pw.WritePreamble(job.preamble()); err != nil {
			return fail(err)
		}
	}

	copyErr := copyRowsTo(ctx, rows, cols, tw, bw, cw, job.prog, &res)
	closeErr := tw.Close()
	flushErr := bw.Flush()
	fileErr := f.Close()
	if err := firstErr(copyErr, closeErr, flushErr, fileErr); err != nil {
		_ = os.Remove(job.target)
		return res, err
	}
	res.Bytes = cw.n
	return res, nil
}

func (j *exportJob) preamble() string {
	return fmt.Sprintf("-- infra-ops 导出\n-- 时间：%s\n-- 来源：%s\n-- 语句：%s\n\n",
		time.Now().Format("2006-01-02 15:04:05"), j.conn.Name, oneLine(j.sql, 300))
}

func newTableWriter(w *bufio.Writer, job *exportJob, cols, types []string) (tableWriter, error) {
	switch job.format {
	case exportFormatSQL:
		return newInsertWriter(w, job.table, cols, types, job.batchRows, job.batchBytes), nil
	case exportFormatCSV:
		return newCSVWriter(w, cols)
	default:
		sheet := job.sheet
		if sheet == "" {
			sheet = "结果"
		}
		return newXLSXWriter(w, sheet, cols, types)
	}
}

// copyRowsTo 逐行喂给 writer；每若干行落盘一次，顺带把进度推给前端。
// 这里不做任何缓存——内存里始终只有当前一行，行数再多也不会把进程撑爆。
func copyRowsTo(ctx context.Context, rows *sql.Rows, cols []string, tw tableWriter,
	bw *bufio.Writer, cw *countingWriter, prog *transferHandle, res *exportResult) error {

	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var n int64
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("导出已中止: %w", err)
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]interface{}, len(cols))
		copy(row, vals) // vals 每轮被 Scan 覆写，必须拷一份给 writer
		if err := tw.WriteRow(row); err != nil {
			return err
		}
		n++
		if n%exportFlushRows == 0 {
			if err := bw.Flush(); err != nil {
				return err
			}
			prog.Running(n, 0, 0, cw.n)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	res.Rows = n
	if bc, ok := tw.(batchCounter); ok {
		res.Batches = bc.Batches()
	}
	return nil
}

// ---------------- 目标表名 ----------------

var fromRe = regexp.MustCompile("(?is)\\bfrom\\s+((?:`[^`]+`|[A-Za-z_$][\\w$]*)(?:\\s*\\.\\s*(?:`[^`]+`|[A-Za-z_$][\\w$]*))?)")

// guessTable 从 SELECT 里猜目标表（取第一个 FROM 后的一到两段标识符）。
// 多表 JOIN 时猜到的可能不是使用者想要的那张，所以前端把结果填进输入框让人改。
func guessTable(sqlText string) (dbName, tableName string) {
	s := blockCommentRe.ReplaceAllString(sqlText, " ")
	s = lineCommentRe.ReplaceAllString(s, "")
	m := fromRe.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	parts := strings.Split(m[1], ".")
	for i := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(parts[i]), "`")
	}
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

// qualifyTable 产出带反引号的限定表名与纯表名。
// want 可以是 "t"、"db.t"；留空则从 SQL 推断。库名优先级：
// want 里显式写的最具体 > 传入的 schema > SQL 里的库名。
func qualifyTable(schema, want, sqlText string) (qualified, plain string) {
	schema = strings.TrimSpace(schema)
	name := strings.TrimSpace(want)
	if name == "" {
		dbName, t := guessTable(sqlText)
		name = t
		if schema == "" {
			schema = dbName
		}
	} else if i := strings.Index(name, "."); i > 0 {
		schema = strings.Trim(name[:i], "` ")
		name = name[i+1:]
	}
	name = strings.Trim(strings.TrimSpace(name), "`")
	if name == "" {
		return "", ""
	}
	if schema == "" {
		return quoteIdent(name), name
	}
	return quoteIdent(schema) + "." + quoteIdent(name), name
}

// normalizeExportTarget 校验并补全导出路径。扩展名与格式不符时补上，
// 避免出现「选了 xlsx 却存成 .txt」这种下次自己都打不开的文件。
func normalizeExportTarget(target, format string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("请先选择导出文件的保存位置")
	}
	if !filepath.IsAbs(target) {
		return "", errors.New("导出路径必须是绝对路径")
	}
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		return "", errors.New("导出路径指向的是目录，请指定文件名")
	}
	ext := "." + format
	if format == exportFormatSQL {
		ext = ".sql"
	}
	if !strings.EqualFold(filepath.Ext(target), ext) {
		target += ext
	}
	dir := filepath.Dir(target)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("目录不存在：%s", dir)
	}
	return target, nil
}

// ---------------- 小工具 ----------------

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// batchCounter 可选接口：分批写出的格式回报批次数。
type batchCounter interface {
	Batches() int64
}

func columnTypes(rows *sql.Rows, n int) []string {
	out := make([]string, n)
	cts, err := rows.ColumnTypes()
	if err != nil {
		return out
	}
	for i := 0; i < n && i < len(cts); i++ {
		out[i] = cts[i].DatabaseTypeName()
	}
	return out
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// oneLine 把可能含换行的 SQL 压成一行：写进 `--` 注释或日志时，
// 换行会让后面的内容跑到注释之外。
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, limit)
}

// transferLogDetail 拼「使用记录」里的详情文本：第一行写清这次操作的来源与去向，
// 后面接原始 SQL，展开记录时能直接看到导出了什么。
func transferLogDetail(action, path, sqlText string) string {
	return fmt.Sprintf("/* %s：%s */\n%s", action, path, sqlText)
}

// recordTransferLog 把导出 / 导入记进「SQL 执行记录」。
//
// 与普通执行共用同一张表：使用者在记录面板里看到的是「这次操作干了什么」，
// 而不是「走的是哪条接口」。记录失败只影响记录本身，绝不改变操作结果。
func (h *Handler) recordTransferLog(conn *model.MySQLConn, schema, sqlType, kind, detail string,
	statements int, rows, affected int64, elapsed time.Duration, err error) {
	if h.logRepo == nil {
		return
	}
	row := model.MySQLSQLLog{
		ConnID:         conn.ID,
		ConnName:       conn.Name,
		SchemaName:     schema,
		SQL:            truncateRunes(detail, logSQLMax),
		StatementCount: statements,
		SQLType:        sqlType,
		StatementKind:  kind,
		Mode:           h.modes.get(conn.ID, defaultMode(conn)),
		Confirmed:      true, // 导出/导入都从显式入口发起，不存在「跳过确认」的情形
		RowCount:       int(rows),
		AffectedRows:   affected,
		ElapsedMs:      elapsed.Milliseconds(),
		Status:         model.LogStatusOK,
	}
	if err != nil {
		row.Status = model.LogStatusError
		row.Error = truncateRunes(err.Error(), logErrMax)
	}
	_, _ = h.logRepo.AddSQLLog(&row)
}
