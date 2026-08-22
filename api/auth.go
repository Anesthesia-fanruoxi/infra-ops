package api

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"infra-ops/common/middleware"
	"infra-ops/common/resp"
	"infra-ops/config"
	"infra-ops/model"
	"infra-ops/store"
)

type authHandler struct {
	cfg       *config.Config
	sessions  *middleware.SessionStore
	auditRepo *store.AuditRepo
}

// NewAuthHandler 创建认证 handler。
func NewAuthHandler(cfg *config.Config, sessions *middleware.SessionStore, auditRepo *store.AuditRepo) *authHandler {
	return &authHandler{cfg: cfg, sessions: sessions, auditRepo: auditRepo}
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login POST /api/v1/auth/login
func (h *authHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}

	if req.Username != h.cfg.Auth.Username {
		h.auditLogin(c, "auth.login_fail", "remote_ip="+c.ClientIP())
		resp.Fail(c, resp.CodeUnauthorized, "用户名或密码错误")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(h.cfg.Auth.PasswordHash), []byte(req.Password)); err != nil {
		h.auditLogin(c, "auth.login_fail", "remote_ip="+c.ClientIP())
		resp.Fail(c, resp.CodeUnauthorized, "用户名或密码错误")
		return
	}

	token := h.sessions.Create(req.Username)
	c.SetCookie(middleware.SessionCookie, token, 12*3600, "/", "", false, true)
	h.auditLogin(c, "auth.login_ok", "user="+req.Username)
	resp.OK(c, gin.H{"username": req.Username})
}

// Logout POST /api/v1/auth/logout
func (h *authHandler) Logout(c *gin.Context) {
	token, _ := c.Cookie(middleware.SessionCookie)
	if token != "" {
		h.sessions.Delete(token)
	}
	c.SetCookie(middleware.SessionCookie, "", -1, "/", "", false, true)
	resp.OK(c, nil)
}

// Me GET /api/v1/auth/me
func (h *authHandler) Me(c *gin.Context) {
	username, _ := c.Get("username")
	resp.OK(c, gin.H{"username": username})
}

func (h *authHandler) auditLogin(c *gin.Context, action, detail string) {
	h.auditRepo.Create(&model.AuditLog{
		Action:   action,
		Detail:   detail,
		RemoteIP: c.ClientIP(),
	})
}

// GeneratePasswordHash 生成 bcrypt 密码哈希。
func GeneratePasswordHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
