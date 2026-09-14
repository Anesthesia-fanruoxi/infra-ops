// Package sftp 负责 SFTP 连接配置的增删改查与文件浏览/上传下载管理。
package sftp

import (
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type Handler struct {
	repo    *repo.SFTPRepo
	cryptoS *crypto.Service
}

func NewHandler(r *repo.SFTPRepo, cs *crypto.Service) *Handler {
	return &Handler{repo: r, cryptoS: cs}
}

// ---------------- 配置 CRUD ----------------

type sftpUpsertReq struct {
	Name     string `json:"name" binding:"required"`
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Key      string `json:"key"`
	Remark   string `json:"remark"`
}

// List GET /api/sftp
func (h *Handler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 SFTP 连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) getByID(c *gin.Context) (*model.SFTPConn, bool) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return nil, false
	}
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "SFTP 连接不存在")
		return nil, false
	}
	return conn, true
}

func encryptIfNeeded(h *Handler, secret, key string) (secB, keyB []byte, err error) {
	if secret != "" {
		secB, err = h.cryptoS.Encrypt([]byte(secret))
		if err != nil {
			return nil, nil, err
		}
	}
	if key != "" {
		keyB, err = h.cryptoS.Encrypt([]byte(key))
		if err != nil {
			return nil, nil, err
		}
	}
	return secB, keyB, nil
}

func (h *Handler) Create(c *gin.Context) {
	var req sftpUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	secB, keyB, err := encryptIfNeeded(h, req.Password, req.Key)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	conn := &model.SFTPConn{
		Name: req.Name, Host: req.Host, Port: req.Port, Username: req.Username,
		EncryptedSecret: secB, EncryptedKey: keyB, Remark: req.Remark,
	}
	id, err := h.repo.Create(conn)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建 SFTP 连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

func (h *Handler) Update(c *gin.Context) {
	_, ok := h.getByID(c)
	if !ok {
		return
	}
	id := shared.ParseID(c)
	var req sftpUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	secB, keyB, err := encryptIfNeeded(h, req.Password, req.Key)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	conn := &model.SFTPConn{
		Name: req.Name, Host: req.Host, Port: req.Port, Username: req.Username,
		EncryptedSecret: secB, EncryptedKey: keyB, Remark: req.Remark,
	}
	if err := h.repo.Update(id, conn); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新 SFTP 连接失败")
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
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除 SFTP 连接失败")
		return
	}
	resp.OK(c, nil)
}
