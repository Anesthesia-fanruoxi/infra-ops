// Package mysql 工具-MySQL：连接配置的增删改查与 SQL 执行 / 元数据查询入口。
package mysql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// ---------------- 配置 CRUD ----------------

type upsertReq struct {
	Name          string `json:"name" binding:"required"`
	Host          string `json:"host" binding:"required"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	DefaultSchema string `json:"default_schema"`
	Charset       string `json:"charset"`
	ReadOnly      *bool  `json:"read_only"` // 默认模式：打开工作台时的初始读写模式，进去后可随时切换
	SSHHostID     int64  `json:"ssh_host_id"`
	Remark        string `json:"remark"`
}

// List GET /api/mysql
func (h *Handler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 MySQL 连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) getByID(c *gin.Context) (*model.MySQLConn, bool) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return nil, false
	}
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "MySQL 连接不存在")
		return nil, false
	}
	return conn, true
}

// buildConn 归一化请求为连接模型；密码为空时保留 nil（更新场景不覆盖原密码）。
func (h *Handler) buildConn(req *upsertReq) (*model.MySQLConn, error) {
	req.Host = strings.TrimSpace(req.Host)
	if req.Port == 0 {
		req.Port = defaultPort
	}
	conn := &model.MySQLConn{
		Name:          strings.TrimSpace(req.Name),
		Host:          req.Host,
		Port:          req.Port,
		Username:      strings.TrimSpace(req.Username),
		DefaultSchema: strings.TrimSpace(req.DefaultSchema),
		Charset:       charsetOf(&model.MySQLConn{Charset: req.Charset}),
		ReadOnly:      true,
		SSHHostID:     req.SSHHostID,
		Remark:        req.Remark,
	}
	if req.ReadOnly != nil {
		conn.ReadOnly = *req.ReadOnly
	}
	if req.Password != "" {
		enc, err := h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			return nil, err
		}
		conn.EncryptedSecret = enc
	}
	return conn, nil
}

// Create POST /api/mysql
func (h *Handler) Create(c *gin.Context) {
	var req upsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Host) == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	conn, err := h.buildConn(&req)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	id, err := h.repo.Create(conn)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建 MySQL 连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/mysql/:id
func (h *Handler) Update(c *gin.Context) {
	if _, ok := h.getByID(c); !ok {
		return
	}
	var req upsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Host) == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	conn, err := h.buildConn(&req)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	if err := h.repo.Update(shared.ParseID(c), conn); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新 MySQL 连接失败")
		return
	}
	resp.OK(c, nil)
}

// Delete DELETE /api/mysql/:id
func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除 MySQL 连接失败")
		return
	}
	// 顺带清掉运行模式与 AI 语义目录，避免自增 ID 复用后继承上一个连接的状态
	h.modes.drop(id)
	if err := h.aiRepo.DropCatalogByConn(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "清理该连接的语义目录失败")
		return
	}
	resp.OK(c, nil)
}

// ---------------- 会话运行模式（门禁判断 1） ----------------

type modeReq struct {
	Mode string `json:"mode" binding:"required"`
}

// GetMode GET /api/mysql/:id/mode —— 取当前会话模式（未设置过则按连接默认模式初始化）。
func (h *Handler) GetMode(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	resp.OK(c, model.MySQLModeState{Mode: h.modes.get(conn.ID, defaultMode(conn)), DefaultMode: defaultMode(conn)})
}

// SetMode PUT /api/mysql/:id/mode —— 连接之后由使用者自行切换读写模式。
func (h *Handler) SetMode(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req modeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Mode != ModeRead && req.Mode != ModeWrite {
		resp.Fail(c, resp.CodeBadRequest, "模式只能是 read 或 write")
		return
	}
	h.modes.set(conn.ID, req.Mode)
	resp.OK(c, model.MySQLModeState{Mode: req.Mode, DefaultMode: defaultMode(conn)})
}

// ---------------- 连接与元数据 ----------------

// Ping GET /api/mysql/:id/ping
func (h *Handler) Ping(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	sess, err := h.open(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), connectTimeout+5*time.Second)
	defer cancel()
	start := time.Now()
	version, err := ping(ctx, sess.db)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": time.Since(start).Milliseconds(), "version": version})
}

// Databases GET /api/mysql/:id/databases
func (h *Handler) Databases(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	sess, err := h.open(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), defaultQueryTimeout)
	defer cancel()
	names, err := listDatabases(ctx, sess.db)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取库列表失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"list": names})
}

// Tables GET /api/mysql/:id/tables?schema=
func (h *Handler) Tables(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	schema := strings.TrimSpace(c.Query("schema"))
	if schema == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少 schema 参数")
		return
	}
	sess, err := h.openForSchema(conn, schema)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer sess.Close()

	items, err := listTables(c.Request.Context(), sess.db, schema)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取表列表失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"list": items})
}

// Columns GET /api/mysql/:id/columns?schema=&table=
func (h *Handler) Columns(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	schema := strings.TrimSpace(c.Query("schema"))
	table := strings.TrimSpace(c.Query("table"))
	if schema == "" || table == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少 schema / table 参数")
		return
	}
	sess, err := h.openForSchema(conn, schema)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer sess.Close()

	ctx := c.Request.Context()
	cols, err := listColumns(ctx, sess.db, schema, table)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取字段失败: "+err.Error())
		return
	}
	indexes, err := listIndexes(ctx, sess.db, schema, table)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取索引失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"columns": cols, "indexes": indexes})
}

// TableDetail GET /api/mysql/:id/table?schema=&table=
//
// 点表即看详情：一次取回概览 + 字段 + 索引 + 建表语句，避免三次往返。
// DDL 取不到（无权限等）时只带 ddl_error，其余内容照常返回。
func (h *Handler) TableDetail(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	schema := strings.TrimSpace(c.Query("schema"))
	table := strings.TrimSpace(c.Query("table"))
	if schema == "" || table == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少 schema / table 参数")
		return
	}
	sess, err := h.openForSchema(conn, schema)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer sess.Close()

	ctx := c.Request.Context()
	detail := &model.MySQLTableDetail{Columns: []model.MySQLColumn{}, Indexes: []model.MySQLIndex{}}

	st, err := tableStatus(ctx, sess.db, schema, table)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取表概览失败: "+err.Error())
		return
	}
	if st != nil {
		detail.Status = *st
	}
	cols, err := listColumns(ctx, sess.db, schema, table)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取字段失败: "+err.Error())
		return
	}
	indexes, err := listIndexes(ctx, sess.db, schema, table)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取索引失败: "+err.Error())
		return
	}
	ddl, ddlErr, _ := showCreateTable(ctx, sess.db, schema, table)
	detail.Columns, detail.Indexes, detail.DDL, detail.DDLErr = cols, indexes, ddl, ddlErr

	resp.OK(c, detail)
}

// ---------------- SQL 执行 ----------------

type queryReq struct {
	SQL       string `json:"sql" binding:"required"`
	Schema    string `json:"schema"`
	Limit     int    `json:"limit"`
	TimeoutMs int    `json:"timeout_ms"`
	Confirmed bool   `json:"confirmed"` // 已读过确认弹窗（对本次请求生效）
}

// Query POST /api/mysql/:id/query
//
// 每次执行都过两道判断：
//
//	判断 1 —— 会话当前是只读还是可写（连接之后由使用者在工作台自行切换）；
//	判断 2 —— 本段 SQL 属于 DQL / SESSION / DML / DDL / OTHER（多语句取风险最高的一条）。
//
// DQL 与 SESSION 直接执行；其余按模式给出分级确认：首次请求只返回 need_confirm，
// 用户确认后带 confirmed 重发。只读模式下的确认仅放行本次，不会切换模式。
func (h *Handler) Query(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req queryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	stmts, an := analyzeSQL(req.SQL)
	if len(stmts) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "SQL 不能为空")
		return
	}

	mode := h.modes.get(conn.ID, defaultMode(conn))
	if an.RequireConfirm() && !req.Confirmed {
		resp.OK(c, gin.H{
			"need_confirm":    true,
			"mode":            mode,
			"sql_type":        an.Type,
			"statement_kind":  an.Kind,
			"statement_count": len(an.Statements),
			"confirm_level":   confirmLevel(mode, an.Type),
			"statements":      an.Statements,
		})
		return
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout(time.Duration(req.TimeoutMs)*time.Millisecond))
	defer cancel()
	// 登记本次执行供「取消」接口中断慢 SQL；请求结束时注销（客户端断开也算取消）
	exec := h.queryCancels.register(conn.ID, cancel)
	defer h.queryCancels.finish(conn.ID, exec)

	sess, err := h.openForSchema(conn, strings.TrimSpace(req.Schema))
	if err != nil {
		// 连不上库也算一次执行尝试：使用者确实点了执行，记录里要能看到（否则会以为没记上）
		h.recordSQLLog(conn, req, an, mode, nil, err, time.Since(started))
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer sess.Close()

	res, err := execStatements(ctx, sess.db, stmts, an, req.Limit)
	if err != nil {
		elapsed := time.Since(started)
		if ctx.Err() == context.DeadlineExceeded {
			h.recordSQLLog(conn, req, an, mode, nil, fmt.Errorf("执行超时，已中断"), elapsed)
			resp.Fail(c, resp.CodeInternal, "执行超时，已中断")
			return
		}
		// 用户点了「取消」（或客户端断开）：不是故障，日志与响应统一成这个文案，
		// 前端据它走轻提示而不是红色错误框
		if ctx.Err() == context.Canceled {
			h.recordSQLLog(conn, req, an, mode, nil, fmt.Errorf("已取消执行"), elapsed)
			resp.Fail(c, resp.CodeInternal, "已取消执行")
			return
		}
		h.recordSQLLog(conn, req, an, mode, nil, err, elapsed)
		resp.Fail(c, resp.CodeInternal, "执行失败: "+err.Error())
		return
	}
	res.Mode = mode
	// 供前端显示「AI 分析」入口：只对单条 EXPLAIN 执行计划放开（其余语句结果不支持分析）
	res.CanAnalyze = explainCanAnalyze(stmts, an)
	h.recordSQLLog(conn, req, an, mode, res, nil, time.Since(started))
	resp.OK(c, res)
}

// CancelQuery POST /api/mysql/:id/query/cancel
//
// 中断本连接上正在执行的查询（慢 SQL 不想等了）。前端点「取消」后，挂起的
// /query 请求会立刻以「已取消执行」返回；没有在执行时返回 canceled=false。
func (h *Handler) CancelQuery(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	resp.OK(c, gin.H{"canceled": h.queryCancels.cancel(conn.ID)})
}

// openForSchema 打开会话并把默认库切到指定 schema。
// 改写 DSN 而非执行 USE：USE 只作用于单条连接，连接池下不可靠。
func (h *Handler) openForSchema(conn *model.MySQLConn, schema string) (*session, error) {
	s := *conn
	if schema != "" {
		s.DefaultSchema = schema
	}
	return h.open(&s)
}
