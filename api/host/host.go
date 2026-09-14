package host

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type Handler struct {
	hostRepo  *repo.HostRepo
	credRepo  *repo.CredentialRepo
	cryptoS   *crypto.Service
	sshC      *sshx.Client
	bus       *eventbus.Bus
	tplRepo   *repo.DeployRepo
	stackRepo *repo.StackRepo
}

type Deps struct {
	HostRepo  *repo.HostRepo
	CredRepo  *repo.CredentialRepo
	CryptoS   *crypto.Service
	SSHC      *sshx.Client
	Bus       *eventbus.Bus
	TplRepo   *repo.DeployRepo
	StackRepo *repo.StackRepo
}

func NewHandler(deps Deps) *Handler {
	return &Handler{
		hostRepo:  deps.HostRepo,
		credRepo:  deps.CredRepo,
		cryptoS:   deps.CryptoS,
		sshC:      deps.SSHC,
		bus:       deps.Bus,
		tplRepo:   deps.TplRepo,
		stackRepo: deps.StackRepo,
	}
}

// TplRepo 暴露部署模板仓库，供路由层复用同一实例。
func (h *Handler) TplRepo() *repo.DeployRepo { return h.tplRepo }

type hostCreateReq struct {
	Name         string `json:"name"` // 可选；缺省用 IP 作为初始名（巡检后会自动跟随系统主机名）
	IP           string `json:"ip" binding:"required"`
	Port         int    `json:"port"`
	Tag          string `json:"tag"`
	LegacyRole   string `json:"role"` // deprecated role alias
	Remark       string `json:"remark"`
	CredentialID int64  `json:"credential_id" binding:"required"`
}

type hostUpdateReq struct {
	Name         string `json:"name"` // 留空保持原名
	IP           string `json:"ip" binding:"required"`
	Port         int    `json:"port"`
	Tag          string `json:"tag"`
	LegacyRole   string `json:"role"` // deprecated role alias
	Remark       string `json:"remark"`
	CredentialID int64  `json:"credential_id" binding:"required"`
}

// List GET /api/v1/hosts
func (h *Handler) List(c *gin.Context) {
	tag := strings.TrimSpace(c.Query("tag"))
	if tag == "" {
		tag = strings.TrimSpace(c.Query("role")) // deprecated query alias
	}
	status := c.Query("status")
	name := c.Query("name")
	ip := c.Query("ip")
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" && name == "" && ip == "" {
		name, ip = keyword, keyword
	}
	page, pageSize := shared.ParsePage(c)
	sortBy := c.Query("sort")
	if sortBy != "" && sortBy != "name" && sortBy != "ip" {
		sortBy = "name"
	}
	order := c.Query("order")
	if order != "desc" {
		order = "asc"
	}

	items, total, err := h.hostRepo.List(tag, status, name, ip, sortBy, order, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询主机失败")
		return
	}
	if items == nil {
		items = []model.Host{}
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

// Get GET /api/v1/hosts/{id}
func (h *Handler) Get(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}
	host, err := h.hostRepo.GetByID(id)
	if err != nil || host == nil {
		resp.Fail(c, resp.CodeNotFound, "主机不存在")
		return
	}
	resp.OK(c, host)
}

// Create POST /api/v1/hosts
func (h *Handler) Create(c *gin.Context) {
	var req hostCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	req.Tag = normalizeHostTag(req.Tag, req.LegacyRole)
	if strings.TrimSpace(req.Name) == "" {
		req.Name = strings.TrimSpace(req.IP) // 初始名 = IP，巡检后自动跟随系统主机名
	}

	// 校验凭据存在
	cred, err := h.credRepo.GetByID(req.CredentialID)
	if err != nil || cred == nil {
		resp.Fail(c, resp.CodeBadRequest, "凭据不存在")
		return
	}

	host := &model.Host{
		Name:         req.Name,
		IP:           req.IP,
		Port:         req.Port,
		Tag:          req.Tag,
		Remark:       req.Remark,
		CredentialID: req.CredentialID,
	}
	id, err := h.hostRepo.Create(host)
	if err != nil {
		if shared.IsDuplicateErr(err) {
			resp.Fail(c, resp.CodeConflict, "主机已存在（IP:端口 重复，或名称与他人冲突）")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建主机失败")
		return
	}
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicHostChanged, map[string]interface{}{"id": id, "action": "create"})
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/v1/hosts/{id}
func (h *Handler) Update(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}
	existing, err := h.hostRepo.GetByID(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "主机不存在")
		return
	}

	var req hostUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Port == 0 {
		req.Port = existing.Port
	}
	req.Tag = normalizeHostTag(req.Tag, req.LegacyRole)
	if strings.TrimSpace(req.Name) == "" {
		req.Name = existing.Name // 留空保持原名
	}

	// 校验凭据存在
	cred, err := h.credRepo.GetByID(req.CredentialID)
	if err != nil || cred == nil {
		resp.Fail(c, resp.CodeBadRequest, "凭据不存在")
		return
	}

	if err := h.hostRepo.Update(id, req.Name, req.IP, req.Port, req.Tag, req.Remark, req.CredentialID); err != nil {
		if shared.IsDuplicateErr(err) {
			resp.Fail(c, resp.CodeConflict, "主机已存在（IP:端口 重复，或名称与他人冲突）")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新主机失败")
		return
	}
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicHostChanged, map[string]interface{}{"id": id, "action": "update"})
	}
	resp.OK(c, nil)
}

// Delete DELETE /api/v1/hosts/{id}
func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}
	if err := h.hostRepo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除主机失败")
		return
	}
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicHostChanged, map[string]interface{}{"id": id, "action": "delete"})
	}
	resp.OK(c, nil)
}

// Installs GET /api/hosts/:id/installs 主机已执行过的安装记录。
func (h *Handler) Installs(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}
	items, err := h.tplRepo.HostInstalls(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询安装记录失败")
		return
	}
	if items == nil {
		items = []model.HostInstall{}
	}
	resp.OK(c, items)
}

// Clusters GET /api/hosts/:id/clusters 主机参与的套件集群列表。
func (h *Handler) Clusters(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}
	if h.stackRepo == nil {
		resp.OK(c, []interface{}{})
		return
	}
	items, err := h.stackRepo.GetHostClusters(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询集群信息失败")
		return
	}
	resp.OK(c, items)
}

// Test POST /api/v1/hosts/{id}/test 连接测试 + 信息采集
func (h *Handler) Test(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的主机 ID")
		return
	}

	host, err := h.hostRepo.GetByID(id)
	if err != nil || host == nil {
		resp.Fail(c, resp.CodeNotFound, "主机不存在")
		return
	}

	result, code := h.testConnection(host)
	if code != 0 {
		resp.Fail(c, code, connectionFailMsg(code))
		return
	}

	// 更新主机状态
	infoJSON, _ := json.Marshal(result.Info)
	if err := h.hostRepo.UpdateProbeResult(id, "online", int(result.LatencyMs), string(infoJSON)); err != nil {
		log.Printf("update probe result: %v", err)
	}

	if h.bus != nil {
		h.bus.Publish(eventbus.TopicHostStatus, map[string]interface{}{
			"id": id, "status": "online", "latency_ms": result.LatencyMs, "info_json": string(infoJSON),
		})
	}
	resp.OK(c, result)
}

// normalizeHostTag trims a tag and defaults blank values to other.
func normalizeHostTag(tag, legacyRole string) string {
	value := strings.TrimSpace(tag)
	if value == "" {
		value = strings.TrimSpace(legacyRole)
	}
	if value == "" {
		return value
	}
	return value
}
