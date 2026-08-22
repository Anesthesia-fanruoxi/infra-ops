// Package router 负责路由表与中间件装配，不含业务逻辑。
package router

import (
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"

	"infra-ops/api"
	"infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/middleware"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/config"
	"infra-ops/store"
)

// Deps 路由所需依赖。
type Deps struct {
	Cfg           *config.Config
	CryptoService *crypto.Service
	SSHClient     *sshx.Client
	Sessions      *middleware.SessionStore
	Bus           *eventbus.Bus
}

// Setup 装配路由并返回 gin.Engine。
func Setup(staticFS fs.FS, deps Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// 审计中间件（全局，只对写操作生效）
	auditRepo := store.NewAuditRepo(deps.Bus)
	r.Use(middleware.Audit(auditRepo))

	// 公开接口（无需鉴权）
	r.GET("/api/healthz", func(c *gin.Context) {
		resp.OK(c, gin.H{"status": "ok"})
	})
	r.GET("/api/version", func(c *gin.Context) {
		resp.OK(c, gin.H{
			"version":    "0.1.0-dev",
			"build_time": "",
			"go_version": "",
		})
	})

	// 认证（login/logout 无需鉴权）
	authHandler := api.NewAuthHandler(deps.Cfg, deps.Sessions, auditRepo)
	auth := r.Group("/api/auth")
	{
		auth.POST("/login", authHandler.Login)
		auth.POST("/logout", authHandler.Logout)
	}

	// 需鉴权的接口
	protected := r.Group("/api")
	protected.Use(middleware.Auth(deps.Sessions))
	protected.GET("/auth/me", authHandler.Me)

	// 凭据管理
	credRepo := store.NewCredentialRepo()
	credHandler := api.NewCredentialHandler(credRepo, deps.CryptoService, deps.Bus)
	cred := protected.Group("/credentials")
	{
		cred.GET("", credHandler.List)
		cred.POST("", credHandler.Create)
		cred.PUT("/:id", credHandler.Update)
		cred.DELETE("/:id", credHandler.Delete)
	}

	// 主机管理
	hostRepo := store.NewHostRepo()
	hostHandler := api.NewHostHandler(api.HostDeps{
		HostRepo: hostRepo,
		CredRepo: credRepo,
		CryptoS:  deps.CryptoService,
		SSHC:     deps.SSHClient,
		Bus:      deps.Bus,
	})
	hosts := protected.Group("/hosts")
	{
		hosts.GET("", hostHandler.List)
		hosts.POST("", hostHandler.Create)
		hosts.POST("/batch", hostHandler.BatchCreate)
		hosts.GET("/:id", hostHandler.Get)
		hosts.PUT("/:id", hostHandler.Update)
		hosts.DELETE("/:id", hostHandler.Delete)
		hosts.POST("/:id/test", hostHandler.Test)
	}

	// 总览 & 审计日志
	miscHandler := api.NewMiscHandler(hostRepo, auditRepo)
	protected.GET("/overview", miscHandler.Overview)
	protected.GET("/audit-logs", miscHandler.AuditLogs)

	// SSE 推送
	sseHandler := api.NewSSEHandler(deps.Bus, hostRepo, credRepo, auditRepo)
	protected.GET("/sse/overview", sseHandler.Overview)
	// ????????????????
	protected.GET("/sse/hosts", sseHandler.HostStatus)

	// 前端静态资源
	if staticFS != nil {
		// 启动时读取 index.html
		indexHTML, _ := fs.ReadFile(staticFS, "index.html")
		r.GET("/", func(c *gin.Context) {
			c.Data(200, "text/html; charset=utf-8", indexHTML)
		})
		// embed FS 中文件在 static/... 下，需要取子目录
		staticSub, err := fs.Sub(staticFS, "static")
		if err == nil {
			r.StaticFS("/static", http.FS(staticSub))
		}
	}

	return r
}
