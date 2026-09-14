package credential

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type Handler struct {
	repo    *repo.CredentialRepo
	cryptoS *crypto.Service
	bus     *eventbus.Bus
}

func NewHandler(repo *repo.CredentialRepo, cs *crypto.Service, bus *eventbus.Bus) *Handler {
	return &Handler{repo: repo, cryptoS: cs, bus: bus}
}

type createReq struct {
	Name     string `json:"name" binding:"required"`
	Type     string `json:"type" binding:"required,oneof=private_key password"`
	Username string `json:"username"`
	Secret   string `json:"secret" binding:"required"`
	Remark   string `json:"remark"`
}

type updateReq struct {
	Name     string `json:"name" binding:"required"`
	Type     string `json:"type" binding:"required,oneof=private_key password"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
	Remark   string `json:"remark"`
}

// List GET /api/credentials
func (h *Handler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := shared.ParsePage(c)

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

// Create POST /api/credentials
func (h *Handler) Create(c *gin.Context) {
	var req createReq
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
		if shared.IsDuplicateErr(err) {
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

// Update PUT /api/credentials/{id}
func (h *Handler) Update(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的凭据 ID")
		return
	}

	existing, err := h.repo.GetByID(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "凭据不存在")
		return
	}

	var req updateReq
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
		if shared.IsDuplicateErr(err) {
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

// Delete DELETE /api/credentials/{id}
func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
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
