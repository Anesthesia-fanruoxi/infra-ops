// Package registry 负责容器镜像仓库连接配置的增删改查与标签/仓库管理。
package registry

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type Handler struct {
	repo    *repo.RegistryRepo
	cryptoS *crypto.Service
}

func NewHandler(repo *repo.RegistryRepo, cs *crypto.Service) *Handler {
	return &Handler{repo: repo, cryptoS: cs}
}

// ---------------- 配置 CRUD ----------------

type registryUpsertReq struct {
	Name     string `json:"name" binding:"required"`
	URL      string `json:"url" binding:"required"`
	Insecure bool   `json:"insecure"`
	AuthType string `json:"auth_type"`
	Username string `json:"username"`
	Password string `json:"password"`
	Remark   string `json:"remark"`
}

func (h *Handler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询镜像仓库连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) Create(c *gin.Context) {
	var req registryUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeRegistryReq(&req)
	var secret []byte
	var err error
	if req.AuthType == "basic" && req.Password != "" {
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	}
	reg := &model.Registry{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: authTypeOrDefault(req.AuthType),
		Username: req.Username, EncryptedSecret: secret, Remark: req.Remark,
	}
	id, err := h.repo.Create(reg)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建镜像仓库连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
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
	var req registryUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeRegistryReq(&req)

	var secret []byte
	if req.AuthType == "basic" && req.Password != "" {
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	}
	// 未修改密码时保留原密文；匿名切换为 basic 但未填密码时清空
	if secret == nil {
		if req.AuthType == "basic" {
			secret = existing.EncryptedSecret
		}
		// 改为匿名则继承 nil → 仅当从 basic 切到 anonymous 且原值存在时显式清空由 auth_type 列承担
		if req.AuthType != "basic" {
			secret = nil
		}
	}
	reg := &model.Registry{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: authTypeOrDefault(req.AuthType), Username: req.Username,
		EncryptedSecret: secret, Remark: req.Remark,
	}
	if err := h.repo.Update(id, reg); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新镜像仓库连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除镜像仓库连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *Handler) resolve(id int64) (*registryClient, int, string) {
	reg, err := h.repo.GetByID(id)
	if err != nil || reg == nil {
		return nil, resp.CodeNotFound, "镜像仓库连接不存在"
	}
	var password []byte
	if reg.AuthType == "basic" && len(reg.EncryptedSecret) > 0 {
		pw, err := h.cryptoS.Decrypt(reg.EncryptedSecret)
		if err != nil {
			return nil, resp.CodeInternal, "解密连接密码失败"
		}
		password = pw
	}
	client, err := newRegistryClient(reg.URL, reg.Insecure, reg.AuthType, reg.Username, string(password))
	if err != nil {
		return nil, resp.CodeBadRequest, "连接地址无效: " + err.Error()
	}
	return client, 0, ""
}

// ---------------- Docker Registry v2 API ----------------

// Ping 测试连通性。
func (h *Handler) Ping(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	start := time.Now()
	respHTTP, _, err := client.do(c.Request.Context(), http.MethodGet, "/v2/", nil, nil)
	latency := time.Since(start).Milliseconds()
	if respHTTP != nil && respHTTP.StatusCode == http.StatusUnauthorized {
		resp.Fail(c, resp.CodeUnauthorized, "认证失败，请检查用户名密码")
		return
	}
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": latency})
}

// Catalog 返回全部仓库（镜像名），后端全量拉取后按关键词过滤。
func (h *Handler) Catalog(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	repos, err := client.catalogAll(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取镜像列表失败: "+err.Error())
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	list := make([]model.RegistryImage, 0, len(repos))
	for _, name := range repos {
		if keyword != "" && !strings.Contains(strings.ToLower(name), keyword) {
			continue
		}
		list = append(list, model.RegistryImage{Name: name})
	}
	resp.OK(c, gin.H{"list": list, "total": len(list)})
}

// Tags 返回指定镜像的全部标签。
func (h *Handler) Tags(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少镜像名")
		return
	}
	tags, err := client.tagsAll(c.Request.Context(), name)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取标签列表失败: "+err.Error())
		return
	}
	// 常见时间推断：粗粒度排序用 server 返回顺序，这里直接返回原始顺序
	resp.OK(c, gin.H{"name": name, "tags": tags})
}

// Manifest 返回镜像某引用（tag 或 digest）的 manifest 详情。
func (h *Handler) Manifest(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	ref := strings.TrimSpace(c.Query("reference"))
	if name == "" || ref == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少镜像名或引用")
		return
	}
	info, err := client.manifest(c.Request.Context(), name, ref)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取镜像信息失败: "+err.Error())
		return
	}
	resp.OK(c, info)
}

// DeleteRepo 删除镜像（reference 为 tag 或 digest）：先解析 digest，再删除对应 manifest。
func (h *Handler) DeleteRepo(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	ref := strings.TrimSpace(c.Query("reference"))
	if name == "" || ref == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少镜像名或引用")
		return
	}
	info, err := client.manifest(c.Request.Context(), name, ref)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "解析镜像引用失败: "+err.Error())
		return
	}
	if err := client.deleteManifest(c.Request.Context(), name, info.Digest, info.MediaType); err != nil {
		resp.Fail(c, resp.CodeInternal, "删除失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{
		"digest": info.Digest,
		"tip":    "镜像已标记删除，需在仓库侧执行垃圾回收(registry GC)才能真正释放存储空间",
	})
}
