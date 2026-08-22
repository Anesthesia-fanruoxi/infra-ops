// Package api 负责参数绑定校验、业务逻辑与对 store/common 的编排。
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

type credentialHandler struct {
	repo    *store.CredentialRepo
	cryptoS *crypto.Service
	bus     *eventbus.Bus
}

func NewCredentialHandler(repo *store.CredentialRepo, cs *crypto.Service, bus *eventbus.Bus) *credentialHandler {
	return &credentialHandler{repo: repo, cryptoS: cs, bus: bus}
}

type credentialCreateReq struct {
	Name     string `json:"name" binding:"required"`
	Type     string `json:"type" binding:"required,oneof=private_key password"`
	Username string `json:"username"`
	Secret   string `json:"secret" binding:"required"`
	Remark   string `json:"remark"`
}

type credentialUpdateReq struct {
	Name     string `json:"name" binding:"required"`
	Type     string `json:"type" binding:"required,oneof=private_key password"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
	Remark   string `json:"remark"`
}

// List GET /api/v1/credentials
func (h *credentialHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := parsePage(c)

	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询凭据失败")
		return
	}

	// 列表不返回密文
	type credView struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Username    string `json:"username"`
		Fingerprint string `json:"fingerprint"`
		Remark      string `json:"remark"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	list := make([]credView, len(items))
	for i, item := range items {
		list[i] = credView{
			ID: item.ID, Name: item.Name, Type: item.Type,
			Username: item.Username, Fingerprint: item.Fingerprint,
			Remark: item.Remark, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		}
	}

	resp.OK(c, resp.PageData{List: list, Total: total, Page: page, PageSize: pageSize})
}

// Create POST /api/v1/credentials
func (h *credentialHandler) Create(c *gin.Context) {
	var req credentialCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Username == "" {
		req.Username = "root"
	}

	encrypted, err := h.cryptoS.Encrypt([]byte(req.Secret))
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}

	fingerprint := ""
	if req.Type == "private_key" {
		fingerprint = calcFingerprint([]byte(req.Secret))
	}

	cred := &model.Credential{
		Name:            req.Name,
		Type:            req.Type,
		Username:        req.Username,
		EncryptedSecret: encrypted,
		Fingerprint:     fingerprint,
		Remark:          req.Remark,
	}

	id, err := h.repo.Create(cred)
	if err != nil {
		if isDuplicateErr(err) {
			resp.Fail(c, resp.CodeConflict, "凭据名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建凭据失败")
		return
	}

	if h.bus != nil {
		h.bus.Publish(eventbus.TopicCredentialChanged, map[string]interface{}{"id": id, "action": "create"})
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/v1/credentials/{id}
func (h *credentialHandler) Update(c *gin.Context) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的凭据 ID")
		return
	}

	existing, err := h.repo.GetByID(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "凭据不存在")
		return
	}

	var req credentialUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Username == "" {
		req.Username = existing.Username
	}

	var encrypted []byte
	fingerprint := existing.Fingerprint
	if req.Secret != "" {
		encrypted, err = h.cryptoS.Encrypt([]byte(req.Secret))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
		if req.Type == "private_key" {
			fingerprint = calcFingerprint([]byte(req.Secret))
		}
	}

	if err := h.repo.Update(id, req.Name, req.Username, req.Remark, fingerprint, encrypted); err != nil {
		if isDuplicateErr(err) {
			resp.Fail(c, resp.CodeConflict, "凭据名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新凭据失败")
		return
	}

	if h.bus != nil {
		h.bus.Publish(eventbus.TopicCredentialChanged, map[string]interface{}{"id": id, "action": "update"})
	}
	resp.OK(c, nil)
}

// Delete DELETE /api/v1/credentials/{id}
func (h *credentialHandler) Delete(c *gin.Context) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的凭据 ID")
		return
	}

	count, err := h.repo.CountByCredentialID(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询引用失败")
		return
	}
	if count > 0 {
		resp.Fail(c, resp.CodeConflict, "该凭据正被 "+strconv.Itoa(count)+" 台主机使用")
		return
	}

	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除凭据失败")
		return
	}

	resp.OK(c, nil)
}

// calcFingerprint 计算私钥 SHA256 指纹（展示用）。
func calcFingerprint(keyData []byte) string {
	h := sha256.Sum256(keyData)
	return hex.EncodeToString(h[:])
}

// parseID 从路径参数解析 ID。
func parseID(c *gin.Context) int64 {
	s := c.Param("id")
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

// parsePage 解析分页参数。
func parsePage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

func isDuplicateErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate name")
}
