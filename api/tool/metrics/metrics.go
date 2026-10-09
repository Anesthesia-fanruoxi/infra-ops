// Package metrics 工具-监控查询：连接 Prometheus / VictoriaMetrics 执行 PromQL 查询，
// 并支持用自然语言生成表达式（AI）与解读查询结果（AI）。
package metrics

import (
	"context"
	"fmt"
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
	repo      *repo.MetricsRepo
	cryptoS   *crypto.Service
	settings  *setting.SettingsRepo
	aiLogRepo *repo.AILogRepo // 统一 AI 调用记录（写 ai_logs，menu=metrics）
	catalog   catalogCache    // 指标清单缓存（AI 生成时作上下文）
}

func NewHandler(r *repo.MetricsRepo, cs *crypto.Service, s *setting.SettingsRepo, aiLog *repo.AILogRepo) *Handler {
	return &Handler{repo: r, cryptoS: cs, settings: s, aiLogRepo: aiLog}
}

// ---------------- 配置 CRUD ----------------

type upsertReq struct {
	Name     string `json:"name" binding:"required"`
	Kind     string `json:"kind"` // prometheus | victoriametrics
	URL      string `json:"url" binding:"required"`
	Insecure bool   `json:"insecure"`
	AuthType string `json:"auth_type"` // none | basic | bearer
	Username string `json:"username"`
	Secret   string `json:"secret"` // basic 的密码 / bearer 的 Token；更新时留空表示不改
	Remark   string `json:"remark"`
}

// normalizeReq 归一化请求：kind / auth_type 只接受已知取值，与所选认证无关的字段清掉。
func normalizeReq(req *upsertReq) {
	if req.Kind != "victoriametrics" {
		req.Kind = "prometheus"
	}
	switch req.AuthType {
	case "basic", "bearer":
	default:
		req.AuthType = "none"
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Username = strings.TrimSpace(req.Username)
	req.Secret = strings.TrimSpace(req.Secret)
	if req.AuthType != "basic" {
		req.Username = ""
	}
	if req.AuthType == "none" {
		req.Secret = ""
	}
}

// buildConn 归一化请求为连接模型；Secret 为空时保留 nil（更新场景不覆盖已存密钥）。
func (h *Handler) buildConn(req *upsertReq) (*model.MetricsConn, error) {
	conn := &model.MetricsConn{
		Name: req.Name, Kind: req.Kind, URL: req.URL, Insecure: req.Insecure,
		AuthType: req.AuthType, Username: req.Username, Remark: req.Remark,
	}
	if req.AuthType != "none" && req.Secret != "" {
		enc, err := h.cryptoS.Encrypt([]byte(req.Secret))
		if err != nil {
			return nil, err
		}
		conn.EncryptedSecret = enc
	}
	return conn, nil
}

// List GET /api/metrics/conns
func (h *Handler) List(c *gin.Context) {
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.List(c.Query("keyword"), page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询监控连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

// Create POST /api/metrics/conns
func (h *Handler) Create(c *gin.Context) {
	var req upsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeReq(&req)
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
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建监控连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/metrics/conns/:id
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
	var req upsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeReq(&req)
	conn, err := h.buildConn(&req)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	// 仍要认证但没填新密钥 → 保留原密文；切到 none → 保持 nil，连密钥一起清掉
	if conn.AuthType != "none" && conn.EncryptedSecret == nil {
		conn.EncryptedSecret = existing.EncryptedSecret
	}
	if err := h.repo.Update(id, conn); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新监控连接失败")
		return
	}
	h.catalog.drop(id) // 端点 / 认证可能已变，指标清单缓存作废
	resp.OK(c, nil)
}

// Delete DELETE /api/metrics/conns/:id
func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除监控连接失败")
		return
	}
	h.catalog.drop(id)
	resp.OK(c, nil)
}

// ---------------- 连接级操作 ----------------

// resolve 按路径参数里的连接 ID 构造客户端；失败时已写响应。
func (h *Handler) resolve(c *gin.Context) (*promClient, *model.MetricsConn, bool) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return nil, nil, false
	}
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "连接不存在")
		return nil, nil, false
	}
	var secret string
	if conn.AuthType != "none" && len(conn.EncryptedSecret) > 0 {
		pw, err := h.cryptoS.Decrypt(conn.EncryptedSecret)
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "解密连接密钥失败")
			return nil, nil, false
		}
		secret = string(pw)
	}
	client, err := newPromClient(conn.URL, conn.Insecure, conn.AuthType, conn.Username, secret)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "连接地址无效: "+err.Error())
		return nil, nil, false
	}
	return client, conn, true
}

// Ping POST /api/metrics/conns/:id/ping 连通性探测（尽力取回版本号）。
func (h *Handler) Ping(c *gin.Context) {
	client, _, ok := h.resolve(c)
	if !ok {
		return
	}
	start := time.Now()
	version, err := client.ping(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": time.Since(start).Milliseconds(), "version": version})
}

// MetricNames GET /api/metrics/conns/:id/metric-names 指标清单（前端表达式提示用）。
func (h *Handler) MetricNames(c *gin.Context) {
	client, _, ok := h.resolve(c)
	if !ok {
		return
	}
	names, err := client.metricNames(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取指标清单失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"list": names, "total": len(names)})
}

// ---------------- 查询 ----------------

// queryReq 查询请求。时间字段的约定：前端传 Unix 毫秒，缺省为「到当前时刻」。
type queryReq struct {
	Expr  string `json:"expr" binding:"required"`
	Mode  string `json:"mode"`  // instant | range（默认 range）
	Start int64  `json:"start"` // Unix 毫秒；range 缺省时倒推 1 小时
	End   int64  `json:"end"`   // Unix 毫秒；缺省为当前时刻
	Step  int64  `json:"step"`  // 秒；<=0 时按窗宽自适应
}

// 单次范围查询的跨度上限：再宽的窗属于拉报表，不该在这个交互工具里跑。
const maxRangeSpanMs = 90 * 24 * 3600 * 1000

// Query POST /api/metrics/conns/:id/query
func (h *Handler) Query(c *gin.Context) {
	client, _, ok := h.resolve(c)
	if !ok {
		return
	}
	var req queryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.Expr = strings.TrimSpace(req.Expr)
	if req.Expr == "" {
		resp.Fail(c, resp.CodeBadRequest, "查询表达式不能为空")
		return
	}
	res, err := runQuery(c.Request.Context(), client, req)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, res)
}

// runQuery 执行一次查询。手动查询、生成后的本地校验与 AI 解读重跑共用这一份参数语义，
// 三处的行为必须一致，不要再各写一份时间换算。
func runQuery(ctx context.Context, client *promClient, req queryReq) (*model.MetricsQueryResult, error) {
	nowMs := time.Now().UnixMilli()
	if req.Mode == "instant" {
		at := req.End
		if at <= 0 {
			at = nowMs
		}
		return client.query(ctx, req.Expr, at)
	}
	end := req.End
	if end <= 0 {
		end = nowMs
	}
	start := req.Start
	if start <= 0 {
		start = end - 3600*1000
	}
	if start >= end {
		return nil, fmt.Errorf("时间范围无效：起始时间必须早于结束时间")
	}
	if end-start > maxRangeSpanMs {
		return nil, fmt.Errorf("时间范围过大（超过 90 天），请缩小后重试")
	}
	step := req.Step
	if step <= 0 {
		step = autoStepSec(start, end)
	}
	return client.queryRange(ctx, req.Expr, start, end, step)
}

// autoStepSec 按窗宽自适应步长：目标约 720 点，最小 15 秒
// （prom 默认单序列 11000 点上限，留有充足余量）。
func autoStepSec(startMs, endMs int64) int64 {
	span := (endMs - startMs) / 1000
	if span <= 0 {
		return 15
	}
	step := span / 720
	if step < 15 {
		step = 15
	}
	return step
}
