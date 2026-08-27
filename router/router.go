// Package router 负责路由表与中间件装配，不含业务逻辑。
package router

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"net/http"
	"time"

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

	// 任务编排
	orchRepo := store.NewOrchestrationRepo()
	orchLogRepo := store.NewOrchestrationLogRepo()
	orchHandler := api.NewOrchHandler(orchRepo, deployRepo, hostRepo, credRepo,
		deps.CryptoService, deps.SSHClient, deps.Bus, auditRepo, orchLogRepo)
	orch := protected.Group("/orchestrations")
	{
		orch.GET("", orchHandler.List)
		orch.POST("", orchHandler.Save)
		orch.GET("/:id", orchHandler.Get)
		orch.PUT("/:id", orchHandler.Save)
		orch.DELETE("/:id", orchHandler.Delete)
		orch.POST("/:id/run", orchHandler.Run)
	}
	protected.GET("/orchestration/runs/:id", orchHandler.RunsDetail)

	// 总览 & 审计日志（审计日志统一走 /api/sse/audits 单一查询流）
	miscHandler := api.NewMiscHandler(hostRepo, auditRepo)
	protected.GET("/overview", miscHandler.Overview)

	// SSE 推送
	sseHandler := api.NewSSEHandler(deps.Bus, hostRepo, credRepo, auditRepo)
	protected.GET("/sse/overview", sseHandler.Overview)
	protected.GET("/sse/hosts", sseHandler.HostStatus)
	protected.GET("/sse/audits", sseHandler.Audits)
	protected.GET("/sse/deploy", deployTaskHandler.SSEProgress)
	protected.GET("/sse/orchestration/steps", orchHandler.SSESteps)
	protected.GET("/sse/orchestration/detail", orchHandler.SSEDetail)

	// 前端静态资源
	if staticFS != nil {
		// 启动时读取 index.html，并注入实例启动时间标记（控制台可见，便于确认浏览器加载的是新构建）
		rawHTML, _ := fs.ReadFile(staticFS, "index.html")
		startStamp := time.Now().Format("2006-01-02 15:04:05")
		indexHTML := strings.ReplaceAll(string(rawHTML), "</head>",
			`<script>console.info("[infra-ops] 实例启动于 `+startStamp+` —— 若看不到此行说明前端未更新")</script></head>`)
		r.GET("/", func(c *gin.Context) {
			// 不加缓存：index.html 引用带内容哈希 ETag 的静态资源，升级后必须重新拉取
			c.Header("Cache-Control", "no-store")
			c.Data(200, "text/html; charset=utf-8", []byte(indexHTML))
		})
		// embed FS 中文件在 static/... 下，需要取子目录
		staticSub, err := fs.Sub(staticFS, "static")
		if err == nil {
			// 嵌入文件修改时间为固定值，http.FileServer 会稳定返回 304 使浏览器沿用旧 JS。
			// 改为基于内容哈希的 ETag + no-store：内容变化即强制重新下载，避免新旧 JS 混用。
			staticServer := http.StripPrefix("/static", &embedFileServer{fs: staticSub})
			r.GET("/static/*filepath", func(c *gin.Context) {
				staticServer.ServeHTTP(c.Writer, c.Request)
			})
		}
	}

	return r
}

// embedFileServer 用内容哈希 ETag 提供嵌入静态文件，避免嵌入文件固定修改时间导致的 304 陈旧缓存。
type embedFileServer struct{ fs fs.FS }

func (s *embedFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upath := strings.TrimPrefix(r.URL.Path, "/")
	f, err := s.fs.Open(upath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read static file failed", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(data)
	etag := fmt.Sprintf("\"%x\"", sum[:16])
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, fi.Name(), time.Time{}, bytesReader(data))
}

// bytesReader 避免额外依赖：io.NopCloser 包装供 ServeContent 使用。
func bytesReader(b []byte) io.ReadSeeker {
	return &bytesReaderT{b: b}
}

type bytesReaderT struct {
	b   []byte
	off int
}

func (r *bytesReaderT) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func (r *bytesReaderT) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.off = int(offset)
	case io.SeekCurrent:
		r.off += int(offset)
	case io.SeekEnd:
		r.off = len(r.b) + int(offset)
	}
	if r.off < 0 {
		r.off = 0
	}
	return int64(r.off), nil
}
