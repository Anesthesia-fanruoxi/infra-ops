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
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
)

// SQL 导入：把一个 .sql 文件流式灌进目标库。
//
// 门禁比手写 SQL 更严，因为一次导入可能改几十万行，出错代价完全不同：
//  1. 必须处于可写模式——不像手写语句那样支持「只读模式本次放行」，一次确认挡不住批量误操作；
//  2. 必须带 confirmed——首次请求只回 need_confirm，让前端把文件与目标库摆出来再确认一次；
//  3. 结构变更语句默认拒绝，要放行得显式开开关。
const (
	defaultImportBatch = 500
	maxImportBatch     = 10000
	maxImportTimeout   = 4 * time.Hour
	maxImportErrors    = 20 // 跳过模式下最多回传多少条失败明细
)

type importReq struct {
	Source      string `json:"source"`  // 本地 .sql 绝对路径
	Schema      string `json:"schema"`  // 目标库；留空取连接默认库
	BatchSize   int    `json:"batch_size"`
	OnError     string `json:"on_error"`     // stop（默认，批事务回滚）/ skip（逐条执行）
	AllowSchema bool   `json:"allow_schema"` // 放行 CREATE / ALTER / DROP 等结构变更
	Confirmed   bool   `json:"confirmed"`
	ProgressKey string `json:"progress_key"`
}

type importResult struct {
	Source     string   `json:"source"`
	Schema     string   `json:"schema"`
	Statements int64    `json:"statements"` // 成功执行条数
	Affected   int64    `json:"affected"`   // 影响行数合计
	Batches    int64    `json:"batches"`
	Skipped    int64    `json:"skipped"` // 跳过模式下失败的条数
	Errors     []string `json:"errors"`
	Bytes      int64    `json:"bytes"` // 实际读取的字节数
	ElapsedMs  int64    `json:"elapsed_ms"`
}

type importJob struct {
	conn        *model.MySQLConn
	source      string
	schema      string
	batchSize   int
	onError     string
	allowSchema bool
	prog        *transferHandle
}

// Import POST /api/mysql/:id/import
func (h *Handler) Import(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req importReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}

	source, err := normalizeImportSource(req.Source)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	schema := strings.TrimSpace(req.Schema)
	if schema == "" {
		schema = conn.DefaultSchema
	}
	onError := strings.ToLower(strings.TrimSpace(req.OnError))
	if onError == "" {
		onError = "stop"
	}
	if onError != "stop" && onError != "skip" {
		resp.Fail(c, resp.CodeBadRequest, "错误策略只能是 stop 或 skip")
		return
	}
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = defaultImportBatch
	}
	if batchSize > maxImportBatch {
		batchSize = maxImportBatch
	}

	mode := h.modes.get(conn.ID, defaultMode(conn))
	if mode != ModeWrite {
		resp.Fail(c, resp.CodeForbidden, "导入会批量修改数据，请先在工作台切换到可写模式")
		return
	}
	if !req.Confirmed {
		// 首次只回确认信息：让前端把「哪个文件、多大、进哪个库」摆出来再让人点一次
		var size int64
		if fi, err := os.Stat(source); err == nil {
			size = fi.Size()
		}
		resp.OK(c, gin.H{
			"need_confirm": true,
			"source":       source,
			"size":         size,
			"schema":       schema,
			"batch_size":   batchSize,
			"on_error":     onError,
			"allow_schema": req.AllowSchema,
		})
		return
	}

	job := &importJob{
		conn: conn, source: source, schema: schema, batchSize: batchSize,
		onError: onError, allowSchema: req.AllowSchema,
	}
	started := time.Now()
	job.prog = h.transfers.begin(req.ProgressKey, "import")
	ctx, cancel := context.WithTimeout(c.Request.Context(), maxImportTimeout)
	defer cancel()

	res, err := h.runImport(ctx, job)
	res.ElapsedMs = time.Since(started).Milliseconds()
	detail := transferLogDetail("从文件导入", source,
		fmt.Sprintf("目标库 %s；每批 %d 条；错误策略 %s", schema, batchSize, onError))
	if err != nil {
		job.prog.Finish(err)
		h.recordTransferLog(conn, schema, model.SQLTypeDML, "IMPORT", detail,
			int(res.Statements), 0, res.Affected, time.Since(started), err)
		resp.Fail(c, resp.CodeInternal, "导入失败: "+err.Error())
		return
	}
	job.prog.Running(res.Affected, res.Statements, res.Batches, res.Bytes)
	job.prog.Finish(nil)
	h.recordTransferLog(conn, schema, model.SQLTypeDML, "IMPORT", detail,
		int(res.Statements), 0, res.Affected, time.Since(started), nil)
	resp.OK(c, res)
}

func (h *Handler) runImport(ctx context.Context, job *importJob) (importResult, error) {
	res := importResult{Source: job.source, Schema: job.schema, Errors: []string{}}

	f, err := os.Open(job.source)
	if err != nil {
		return res, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()
	cr := &countingReader{r: f}
	br := bufio.NewReaderSize(cr, 64*1024)
	// UTF-8 BOM：留着会成为首条语句的开头字符，让整条语句语法错误
	if head, err := br.Peek(3); err == nil && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF {
		_, _ = br.Discard(3)
	}

	sess, err := h.openForSchema(job.conn, job.schema)
	if err != nil {
		return res, err
	}
	defer sess.Close()

	sc := newSQLScanner(br, maxStatementBytes)
	runner := sqlTxRunner{db: sess.db}
	if job.onError == "skip" {
		err = h.importStatementByStatement(ctx, runner, sc, job, &res)
	} else {
		err = h.importInBatches(ctx, runner, sc, job, &res)
	}
	res.Bytes = cr.n
	return res, err
}

// 导入执行面的窄接口：*sql.DB 与 *sql.Tx 都满足 ExecContext，
// 把它们抽出来是为了让「分批边界、批内失败回滚、跳过策略」这些最容易写错的
// 编排逻辑能用假实现直接测——真库只在端到端再验一次。
type importExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type importTx interface {
	importExecer
	Commit() error
	Rollback() error
}

type importTxRunner interface {
	importExecer
	begin(ctx context.Context) (importTx, error)
}

type sqlTxRunner struct{ db *sql.DB }

func (r sqlTxRunner) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return r.db.ExecContext(ctx, query, args...)
}

func (r sqlTxRunner) begin(ctx context.Context) (importTx, error) { return r.db.BeginTx(ctx, nil) }

// importInBatches 按批走事务：一批要么全成、要么全回滚。
// 这是默认策略——批量导入最怕「改了一半不知道改到哪」。
func (h *Handler) importInBatches(ctx context.Context, runner importTxRunner, sc *sqlScanner,
	job *importJob, res *importResult) error {

	batch := make([]string, 0, job.batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		tx, err := runner.begin(ctx)
		if err != nil {
			return err
		}
		var affected int64
		for i, stmt := range batch {
			r, execErr := tx.ExecContext(ctx, stmt)
			if execErr != nil {
				_ = tx.Rollback()
				return fmt.Errorf("第 %d 条语句执行失败（本批共 %d 条，已整体回滚）：%v\n语句开头：%s",
					res.Statements+int64(i)+1, len(batch), execErr, oneLine(stmt, 120))
			}
			if n, e := r.RowsAffected(); e == nil {
				affected += n
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		res.Statements += int64(len(batch))
		res.Affected += affected
		res.Batches++
		batch = batch[:0]
		job.prog.Running(res.Affected, res.Statements, res.Batches, 0)
		return nil
	}

	for {
		stmt, err := sc.Next()
		if err == io.EOF {
			return flush()
		}
		if err != nil {
			return err
		}
		kind, sqlType := classifySQL(stmt)
		if err := checkImportStatement(kind, sqlType, sc.Lines(), stmt, job.allowSchema); err != nil {
			return err
		}
		// DDL 会隐式提交事务：先把手里的批落掉再单独执行，语义才不会含糊
		if sqlType == model.SQLTypeDDL {
			if err := flush(); err != nil {
				return err
			}
			if _, err := runner.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("第 %d 条语句（%s）执行失败：%v", res.Statements+1, kind, err)
			}
			res.Statements++
			res.Batches++
			job.prog.Running(res.Affected, res.Statements, res.Batches, 0)
			continue
		}
		batch = append(batch, stmt)
		if len(batch) >= job.batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// importStatementByStatement 逐条执行（自动提交），失败的单条跳过并记账。
// 这条路径比批量慢一个数量级——没有事务包裹，每条都要单独往返服务端——
// 但只要目的是「尽量把能进的都进了」，它就是对的。
func (h *Handler) importStatementByStatement(ctx context.Context, runner importTxRunner, sc *sqlScanner,
	job *importJob, res *importResult) error {

	for {
		stmt, err := sc.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		no := sc.Lines()
		kind, sqlType := classifySQL(stmt)
		if err := checkImportStatement(kind, sqlType, no, stmt, job.allowSchema); err != nil {
			return err
		}
		r, execErr := runner.ExecContext(ctx, stmt)
		if execErr != nil {
			res.Skipped++
			if len(res.Errors) < maxImportErrors {
				res.Errors = append(res.Errors,
					fmt.Sprintf("第 %d 条：%v（语句开头 %s）", no, execErr, oneLine(stmt, 80)))
			}
			continue
		}
		res.Statements++
		if n, e := r.RowsAffected(); e == nil {
			res.Affected += n
		}
		if res.Statements%int64(job.batchSize) == 0 {
			job.prog.Running(res.Affected, res.Statements, 0, 0)
		}
	}
}

// checkImportStatement 只放行查询 / 会话 / 数据变更三类。
//
// 结构变更与无法归类的语句（存储过程、权限操作等）默认拒绝：一个几万条的 dump 里
// 混进一条 DROP 是看不出来的，批量通道不能靠人眼逐条确认——要执行就得显式开开关，
// 那一下勾选就是「我知道里面可能有结构变更」的承诺。
func checkImportStatement(kind, sqlType string, no int, stmt string, allowSchema bool) error {
	switch sqlType {
	case model.SQLTypeDQL, model.SQLTypeSession, model.SQLTypeDML:
		return nil
	case model.SQLTypeDDL:
		if allowSchema {
			return nil
		}
		return fmt.Errorf("第 %d 条语句是 %s（结构变更），导入通道默认不执行；"+
			"确认要执行请勾选「允许结构变更语句」。语句开头：%s", no, kind, oneLine(stmt, 120))
	default:
		return fmt.Errorf("第 %d 条语句是 %s，无法归类为查询 / 会话 / 数据变更，导入通道不执行。"+
			"语句开头：%s", no, kind, oneLine(stmt, 120))
	}
}

// normalizeImportSource 校验导入文件。这里只保证「是一个能读的普通文件」，
// 不限制扩展名——有人会把 dump 存成 .txt，卡后缀只会平添麻烦。
func normalizeImportSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("请先选择要导入的 SQL 文件")
	}
	if !filepath.IsAbs(source) {
		return "", errors.New("导入路径必须是绝对路径")
	}
	st, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("读取文件失败：%v", err)
	}
	if st.IsDir() {
		return "", errors.New("导入路径指向的是目录，请选择文件")
	}
	return source, nil
}

// countingReader 统计真正从文件读走的字节数（bufio 会预读，用文件偏移量不准）。
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
