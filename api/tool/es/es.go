// Package es 负责 Elasticsearch 连接配置的增删改查与检索/索引管理。
package es

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
	"infra-ops/store/setting"
)

type Handler struct {
	repo     *repo.ESRepo
	cryptoS  *crypto.Service
	viewRepo *repo.ESViewRepo
	// AI 相关依赖（Grok 解析的 AI 生成用）：平台级 AI 配置 + 统一调用记录
	settings  *setting.SettingsRepo
	aiLogRepo *repo.AILogRepo
	// 「系统自带字段」缓存：生成 logstash 配置时读它决定 remove_field，不现打集群
	fieldRepo *repo.ESFieldRepo
	// 导入任务：进度注册表与取消登记（见 import_progress.go / import_cancel.go）
	imports *importProgress
	cancels *importCancelRegistry
	pauses  *importPauses
}

func NewHandler(repo *repo.ESRepo, cs *crypto.Service) *Handler {
	return &Handler{
		repo:    repo,
		cryptoS: cs,
		imports: newImportProgress(),
		cancels: newImportCancelRegistry(),
		pauses:  newImportPauses(),
	}
}

// WithViewRepo 注入数据视图仓储（视图 CRUD 与同步依赖）。
func (h *Handler) WithViewRepo(vr *repo.ESViewRepo) *Handler {
	h.viewRepo = vr
	return h
}

// WithAI 注入 AI 依赖（Grok 解析的 pattern 生成依赖平台 AI 配置与统一调用记录）。
func (h *Handler) WithAI(s *setting.SettingsRepo, aiLog *repo.AILogRepo) *Handler {
	h.settings = s
	h.aiLogRepo = aiLog
	return h
}

// WithFieldCache 注入「系统自带字段」缓存仓储。
// 生成 logstash 配置只读它、不自己去打集群 —— 探测是后台的活（见 field_cache.go）。
func (h *Handler) WithFieldCache(fr *repo.ESFieldRepo) *Handler {
	h.fieldRepo = fr
	return h
}

// ---------------- 配置 CRUD ----------------

type esUpsertReq struct {
	Name     string `json:"name" binding:"required"`
	URL      string `json:"url" binding:"required"`
	Insecure bool   `json:"insecure"`
	AuthType string `json:"auth_type"`
	Username string `json:"username"`
	Password string `json:"password"`
	Remark   string `json:"remark"`
}

func (h *Handler) List(c *gin.Context) {
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.List(c.Query("keyword"), page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 ES 连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) Create(c *gin.Context) {
	var req esUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeESReq(&req)
	var secret []byte
	if req.AuthType == "basic" && req.Password != "" {
		var err error
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	}
	id, err := h.repo.Create(&model.ESConn{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: esAuthType(req.AuthType), Username: req.Username,
		EncryptedSecret: secret, Remark: req.Remark,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建 ES 连接失败")
		return
	}
	h.probeFieldsAsync(id)
	resp.OK(c, gin.H{"id": id})
}

// probeFieldsAsync 异步探测某连接的「系统自带字段」并落库。
// 新建连接后立刻探一次 —— 等 24h 后的后台轮次太晚，用户马上就会去生成配置。
// 异步执行不拖慢创建响应；失败也无妨，后台轮次会重试，界面会显示失败原因。
func (h *Handler) probeFieldsAsync(connID int64) {
	if h.fieldRepo == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), esFieldProbeTimeout)
		defer cancel()
		conn, err := h.repo.GetByID(connID)
		if err != nil || conn == nil {
			return
		}
		_ = probeAndSave(ctx, h.cryptoS, h.fieldRepo, conn)
	}()
}

func (h *Handler) Update(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	existing, err := h.repo.GetByID(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "连接不存在")
		return
	}
	var req esUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeESReq(&req)

	var secret []byte
	if req.AuthType == "basic" && req.Password != "" {
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	} else if req.AuthType == "basic" {
		secret = existing.EncryptedSecret
	} // anonymous → 保持 nil 清空
	reg := &model.ESConn{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: esAuthType(req.AuthType), Username: req.Username,
		EncryptedSecret: secret, Remark: req.Remark,
	}
	if err := h.repo.Update(id, reg); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新 ES 连接失败")
		return
	}
	// 地址 / 认证可能变了，缓存里的版本与字段清单就未必还成立 —— 异步重探一次
	h.probeFieldsAsync(id)
	resp.OK(c, nil)
}

func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除 ES 连接失败")
		return
	}
	if h.fieldRepo != nil {
		_ = h.fieldRepo.Delete(id) // 顺带清掉字段缓存，不留孤儿行
	}
	resp.OK(c, nil)
}

func (h *Handler) resolve(id int64) (*escClient, int, string) {
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		return nil, resp.CodeNotFound, "ES 连接不存在"
	}
	var password []byte
	if conn.AuthType == "basic" && len(conn.EncryptedSecret) > 0 {
		pw, err := h.cryptoS.Decrypt(conn.EncryptedSecret)
		if err != nil {
			return nil, resp.CodeInternal, "解密连接密码失败"
		}
		password = pw
	}
	client, err := newESCClient(conn.URL, conn.Insecure, conn.AuthType, conn.Username, string(password))
	if err != nil {
		return nil, resp.CodeBadRequest, "连接地址无效: " + err.Error()
	}
	return client, 0, ""
}

// ---------------- ES handlers ----------------

// Ping 测试连通性。
func (h *Handler) Ping(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	start := time.Now()
	if err := client.ping(c.Request.Context()); err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": time.Since(start).Milliseconds()})
}

// Overview 集群概览：版本 + 健康。
func (h *Handler) Overview(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	ov, err := client.overview(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取集群概览失败: "+err.Error())
		return
	}
	resp.OK(c, ov)
}

// Nodes 节点资源列表。
func (h *Handler) Nodes(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	nodes, err := client.nodes(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取节点信息失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"list": nodes})
}

// Indices 索引列表。q 为可选的索引名通配（* ? 与逗号分隔多段），
// 交给集群在全部索引里匹配；无论匹配多少，list 最多回前 100 条，total 回全部条数，
// 界面据此显示「共 N 个 · 展示前 100」并在需要时提示用搜索缩小范围。
func (h *Handler) Indices(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	list, total, err := client.indices(c.Request.Context(), q)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取索引列表失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{
		"list":      list,
		"total":     total,
		"returned":  len(list),
		"truncated": total > len(list),
		"pattern":   q,
	})
}

// IndicesTotal GET /api/es/:id/indices/total
// 业务索引总数（剔除 . 系统索引）。索引页把「列表」与「总数」拆成两个请求：
// 列表先到先渲染，总数独立异步返回（慢一步也不挡表格），前端在总数位转圈等待。
func (h *Handler) IndicesTotal(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	total, err := client.indicesTotal(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取索引总数失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"total": total})
}

// CreateIndex 创建索引。
func (h *Handler) CreateIndex(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		Index    string `json:"index" binding:"required"`
		Shards   *int   `json:"shards"`
		Replicas *int   `json:"replicas"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := client.createIndex(c.Request.Context(), req.Index, req.Shards, req.Replicas); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, nil)
}

// DeleteIndex 删除索引。
func (h *Handler) DeleteIndex(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	index := strings.TrimSpace(c.Param("index"))
	if index == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少索引名")
		return
	}
	if err := client.deleteIndex(c.Request.Context(), index); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, nil)
}

// Search 文档查询。
func (h *Handler) Search(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		Index string          `json:"index" binding:"required"`
		From  int             `json:"from"`
		Size  int             `json:"size"`
		Query json.RawMessage `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.From < 0 {
		req.From = 0
	}
	if req.Size <= 0 || req.Size > 1000 {
		req.Size = 20
	}
	result, err := client.search(c.Request.Context(), req.Index, req.From, req.Size, req.Query)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, result)
}

// ---------------- ES HTTP 客户端 ----------------
