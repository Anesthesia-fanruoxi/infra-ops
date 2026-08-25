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
	"infra-ops/store"
)

// Deps 路由所需依赖。
type Deps struct {
	Settings          *store.SettingsRepo
	CryptoService     *crypto.Service
	SSHClient         *sshx.Client
	Sessions          *middleware.SessionStore
	Bus               *eventbus.Bus
	DeployConcurrency int // <=0 表示按主机数自适应
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
	authHandler := api.NewAuthHandler(deps.Settings, deps.Sessions, auditRepo)
	auth := r.Group("/api/auth")
	{
		auth.POST("/login", authHandler.Login)
		auth.POST("/logout", authHandler.Logout)
	}

	// 需鉴权的接口；待改密时仅放行改密相关接口
	protected := r.Group("/api")
	protected.Use(middleware.Auth(deps.Sessions))
	protected.GET("/auth/me", authHandler.Me)
	protected.POST("/auth/password", authHandler.ChangePassword)
	protected.Use(middleware.RequirePasswordChanged(deps.Settings, store.SettingAuthMustChange,
		"/api/auth/password", "/api/auth/me", "/api/auth/logout"))

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
		TplRepo:  store.NewDeployRepo(),
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
		hosts.GET("/:id/installs", hostHandler.Installs)
	}

	// 部署中心
	deployRepo := store.NewDeployRepo()
	scheduleRepo := store.NewDeployScheduleRepo()
	deployTplHandler := api.NewDeployTemplateHandler(deployRepo, scheduleRepo)
	tpl := protected.Group("/deploy/templates")
	{
		tpl.GET("", deployTplHandler.List)
		tpl.POST("", deployTplHandler.Create)
		tpl.PUT("/:id", deployTplHandler.Update)
		tpl.DELETE("/:id", deployTplHandler.Delete)
	}
	deployTaskHandler := api.NewDeployHandler(deployRepo, scheduleRepo, hostRepo, credRepo,
		deps.CryptoService, deps.SSHClient, deps.Bus, auditRepo, deps.DeployConcurrency)
	deployTaskHandler.StartScheduler()
	deploySchedHandler := api.NewDeployScheduleHandler(scheduleRepo, deployRepo, deployTaskHandler)
	protected.POST("/deploy/run", deployTaskHandler.Run)
	protected.GET("/deploy/tasks", deployTaskHandler.Tasks)
	protected.GET("/deploy/tasks/:id", deployTaskHandler.TaskDetail)
	sched := protected.Group("/deploy/schedules")
	{
		sched.GET("", deploySchedHandler.List)
		sched.POST("", deploySchedHandler.Create)
		sched.PUT("/:id", deploySchedHandler.Update)
		sched.DELETE("/:id", deploySchedHandler.Delete)
		sched.POST("/:id/toggle", deploySchedHandler.Toggle)
		sched.GET("/:id/runs", deploySchedHandler.Runs)
	}

	// 总览 & 审计日志（审计日志统一走 /api/sse/audits 单一查询流）
	miscHandler := api.NewMiscHandler(hostRepo, auditRepo)
	protected.GET("/overview", miscHandler.Overview)

	// SSE 推送
	sseHandler := api.NewSSEHandler(deps.Bus, hostRepo, credRepo, auditRepo)
	protected.GET("/sse/overview", sseHandler.Overview)
	protected.GET("/sse/hosts", sseHandler.HostStatus)
	protected.GET("/sse/audits", sseHandler.Audits)
	protected.GET("/sse/deploy", deployTaskHandler.SSEProgress)

	// 前端静态资源
	if staticFS != nil {
		// 启动时读取 index.html
		indexHTML, _ := fs.ReadFile(staticFS, "index.html")
		r.GET("/", func(c *gin.Context) {
			c.Header("Cache-Control", "no-cache")
			c.Data(200, "text/html; charset=utf-8", indexHTML)
		})
		// embed FS 中文件在 static/... 下，需要取子目录
		staticSub, err := fs.Sub(staticFS, "static")
		if err == nil {
			// no-cache 强制浏览器每次升级后重新校验（配合 Last-Modified 返回 304），避免新旧 JS 混用
			staticServer := http.StripPrefix("/static", http.FileServer(http.FS(staticSub)))
			r.GET("/static/*filepath", func(c *gin.Context) {
				c.Header("Cache-Control", "no-cache")
				staticServer.ServeHTTP(c.Writer, c.Request)
			})
		}
	}

	return r
}
