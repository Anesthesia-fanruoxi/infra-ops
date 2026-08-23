package api

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"infra-ops/common/middleware"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

type authHandler struct {
	settings  *store.SettingsRepo
	sessions  *middleware.SessionStore
	auditRepo *store.AuditRepo
}

// NewAuthHandler 创建认证 handler。
func NewAuthHandler(settings *store.SettingsRepo, sessions *middleware.SessionStore, auditRepo *store.AuditRepo) *authHandler {
	return &authHandler{settings: settings, sessions: sessions, auditRepo: auditRepo}
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type changePwdReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// Login POST /api/auth/login
func (h *authHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}

	username, err := h.settings.Get(store.SettingAuthUsername)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取账号配置失败")
		return
	}
	hash, err := h.settings.Get(store.SettingAuthPasswordHash)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取账号配置失败")
		return
	}
	mustChange, _ := h.settings.Get(store.SettingAuthMustChange)

	if req.Username != username ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		h.auditLogin(c, "auth.login_fail", "remote_ip="+c.ClientIP())
		resp.Fail(c, resp.CodeUnauthorized, "用户名或密码错误")
		return
	}

	token := h.sessions.Create(req.Username)
	c.SetCookie(middleware.SessionCookie, token, 12*3600, "/", "", false, true)
	h.auditLogin(c, "auth.login_ok", "user="+req.Username)
	resp.OK(c, gin.H{"username": req.Username, "must_change_password": mustChange == "1"})
}

// ChangePassword POST /api/auth/password：校验旧密码后更新，并清除强制改密标记。
func (h *authHandler) ChangePassword(c *gin.Context) {
	var req changePwdReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误：新密码至少8位")
		return
	}
	username, _ := c.Get("username")

	hash, err := h.settings.Get(store.SettingAuthPasswordHash)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取账号配置失败")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)) != nil {
		resp.Fail(c, resp.CodeUnauthorized, "旧密码错误")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "生成密码哈希失败")
		return
	}
	if err := h.settings.Set(store.SettingAuthPasswordHash, string(newHash)); err != nil {
		resp.Fail(c, resp.CodeInternal, "保存新密码失败")
		return
	}
	if err := h.settings.Set(store.SettingAuthMustChange, "0"); err != nil {
		resp.Fail(c, resp.CodeInternal, "更新改密标记失败")
		return
	}

	h.auditLogin(c, "auth.password_change", "user="+toString(username))
	resp.OK(c, nil)
}

// Logout POST /api/auth/logout
func (h *authHandler) Logout(c *gin.Context) {
	token, _ := c.Cookie(middleware.SessionCookie)
	if token != "" {
		h.sessions.Delete(token)
	}
	c.SetCookie(middleware.SessionCookie, "", -1, "/", "", false, true)
	resp.OK(c, nil)
}

// Me GET /api/auth/me
func (h *authHandler) Me(c *gin.Context) {
	username, _ := c.Get("username")
	mustChange, _ := h.settings.Get(store.SettingAuthMustChange)
	resp.OK(c, gin.H{"username": username, "must_change_password": mustChange == "1"})
}

func (h *authHandler) auditLogin(c *gin.Context, action, detail string) {
	h.auditRepo.Create(&model.AuditLog{
		Action:   action,
		Detail:   detail,
		RemoteIP: c.ClientIP(),
	})
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
