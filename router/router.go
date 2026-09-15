// Package router 负责路由表与中间件装配，不含业务逻辑。
package router

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/auth"
	"infra-ops/api/credential"
	"infra-ops/api/deploy"
	"infra-ops/api/host"
	"infra-ops/api/orchestration"
	"infra-ops/api/overview"
	"infra-ops/api/sse"
	"infra-ops/api/stack"
	esapi "infra-ops/api/tool/es"
	regapi "infra-ops/api/tool/registry"
	sftpapi "infra-ops/api/tool/sftp"
	"infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/middleware"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/store/repo"
	"infra-ops/store/setting"
)

// Deps 路由所需依赖。
type Deps struct {
	Settings          *setting.SettingsRepo
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
	auditRepo := repo.NewAuditRepo(deps.Bus)
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
	authHandler := auth.NewHandler(deps.Settings, deps.Sessions, auditRepo)
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
	protected.Use(middleware.RequirePasswordChanged(deps.Settings, setting.SettingAuthMustChange,
		"/api/auth/password", "/api/auth/me", "/api/auth/logout"))

	// 凭据管理
	credRepo := repo.NewCredentialRepo()
	credHandler := credential.NewHandler(credRepo, deps.CryptoService, deps.Bus)
	cred := protected.Group("/credentials")
	{
		cred.GET("", credHandler.List)
		cred.POST("", credHandler.Create)
		cred.PUT("/:id", credHandler.Update)
		cred.DELETE("/:id", credHandler.Delete)
	}

	// 主机管理
	hostRepo := repo.NewHostRepo()
	hostHandler := host.NewHandler(host.Deps{
		HostRepo:  hostRepo,
		CredRepo:  credRepo,
		CryptoS:   deps.CryptoService,
		SSHC:      deps.SSHClient,
		Bus:       deps.Bus,
		TplRepo:   repo.NewDeployRepo(),
		StackRepo: repo.NewStackRepo(),
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
		hosts.GET("/:id/clusters", hostHandler.Clusters)
		hosts.GET("/:id/services", overview.NewServiceHandler(hostHandler.TplRepo()).HostList)
	}

	// 服务清单：总览聚合入口
	protected.GET("/services", overview.NewServiceHandler(hostHandler.TplRepo()).List)

	// 部署中心
	deployRepo := repo.NewDeployRepo()
	scheduleRepo := repo.NewDeployScheduleRepo()
	deployTplHandler := deploy.NewDeployTemplateHandler(deployRepo, scheduleRepo)
	tpl := protected.Group("/deploy/templates")
	{
		tpl.GET("", deployTplHandler.List)
		tpl.POST("", deployTplHandler.Create)
		tpl.PUT("/:id", deployTplHandler.Update)
		tpl.DELETE("/:id", deployTplHandler.Delete)
	}
	deployTaskHandler := deploy.NewDeployHandler(deployRepo, scheduleRepo, hostRepo, credRepo,
		deps.CryptoService, deps.SSHClient, deps.Bus, auditRepo, deps.DeployConcurrency)
	deployTaskHandler.StartScheduler()
	deploySchedHandler := deploy.NewDeployScheduleHandler(scheduleRepo, deployRepo, deployTaskHandler)
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
	orchRepo := repo.NewOrchestrationRepo()
	orchLogRepo := repo.NewOrchestrationLogRepo()
	orchHandler := orchestration.NewOrchHandler(orchRepo, deployRepo, hostRepo, credRepo,
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

	// 套件部署
	stackHandler := stack.NewStackHandler(repo.NewStackRepo(), deployRepo, hostRepo, credRepo,
		deps.CryptoService, deps.SSHClient, deps.Bus, auditRepo, deps.DeployConcurrency)
	stacks := protected.Group("/stacks")
	{
		stacks.GET("", stackHandler.List)
		stacks.POST("/preflight", stackHandler.Preflight)
		stacks.POST("/run", stackHandler.Run)
		stacks.GET("/runs", stackHandler.Runs)
		stacks.GET("/runs/:id", stackHandler.RunDetail)
		stacks.GET("/instances", stackHandler.ListInstances)
		stacks.GET("/instances/:id", stackHandler.GetInstance)
		stacks.PATCH("/instances/:id", stackHandler.PatchInstance)
		stacks.DELETE("/instances/:id", stackHandler.DeleteInstance)
		stacks.POST("/instances/:id/scale-out", stackHandler.ScaleOut)
		stacks.POST("/instances/:id/scale-in", stackHandler.ScaleIn)
		stacks.POST("/instances/:id/add-component", stackHandler.AddComponent)
		stacks.POST("/instances/:id/remove-component", stackHandler.RemoveComponent)
		stacks.POST("/instances/:id/uninstall", stackHandler.Uninstall)
		stacks.POST("/instances/:id/reinstall", stackHandler.Reinstall)
		stacks.POST("/instances/:id/verify", stackHandler.VerifyInstance)
		stacks.GET("/instances/:id/ca", stackHandler.CaCert)
		stacks.GET("/instances/:id/runs", stackHandler.InstanceRuns)
		// 角色计划（docs/角色物化设计.md §4.5）
		stacks.POST("/plan/preview", stackHandler.PlanPreview)
		stacks.GET("/instances/:id/plan", stackHandler.GetPlan)
		stacks.POST("/instances/:id/replan", stackHandler.ReplanPlan)
	}

	// 部署资产：套件离线物料（上传 / 服务端代下 / 就绪检查），部署时经 SFTP 分发
	assetHandler := stack.NewAssetHandler()
	assets := protected.Group("/stacks/assets")
	{
		assets.GET("", assetHandler.List)
		assets.POST("/check", assetHandler.Check)
		assets.POST("/upload", assetHandler.Upload)
		assets.POST("/fetch", assetHandler.Fetch)
		assets.DELETE("/:id", assetHandler.Delete)
	}

	// 工具-镜像仓库（Docker Registry）
	regRepo := repo.NewRegistryRepo()
	regHandler := regapi.NewHandler(regRepo, deps.CryptoService)
	reg := protected.Group("/registry")
	{
		reg.GET("", regHandler.List)
		reg.POST("", regHandler.Create)
		reg.PUT("/:id", regHandler.Update)
		reg.DELETE("/:id", regHandler.Delete)
		reg.GET("/:id/ping", regHandler.Ping)
		reg.GET("/:id/catalog", regHandler.Catalog)
		reg.GET("/:id/tags", regHandler.Tags)
		reg.GET("/:id/manifest", regHandler.Manifest)
		reg.DELETE("/:id/repo", regHandler.DeleteRepo)
	}

	// 工具-Elasticsearch 连接与浏览
	esRepo := repo.NewESRepo()
	esViewRepo := repo.NewESViewRepo()
	esHandler := esapi.NewHandler(esRepo, deps.CryptoService).WithViewRepo(esViewRepo).WithAuditRepo(auditRepo)
	es := protected.Group("/es")
	{
		es.GET("", esHandler.List)
		es.POST("", esHandler.Create)
		es.PUT("/:id", esHandler.Update)
		es.DELETE("/:id", esHandler.Delete)
		es.GET("/:id/ping", esHandler.Ping)
		es.GET("/:id/overview", esHandler.Overview)
		es.GET("/:id/nodes", esHandler.Nodes)
		es.GET("/:id/indices", esHandler.Indices)
		es.GET("/:id/indices/:index/mapping", esHandler.IndexMapping)
		es.POST("/:id/index", esHandler.CreateIndex)
		es.DELETE("/:id/index/:index", esHandler.DeleteIndex)
		// 检索（语义已变更：view_id + kql，内存分页；§14.4/§14.5）
		es.POST("/:id/search", esHandler.SearchKQL)
		es.POST("/:id/search/page", esHandler.SearchPage)
		es.POST("/:id/kql/validate", esHandler.ValidateKQL)
		// 数据视图
		es.POST("/:id/index-pattern/probe", esHandler.Probe)
		es.GET("/:id/views", esHandler.ListViews)
		es.POST("/:id/views", esHandler.CreateView)
		es.GET("/:id/views/:vid", esHandler.GetView)
		es.PUT("/:id/views/:vid", esHandler.UpdateView)
		es.DELETE("/:id/views/:vid", esHandler.DeleteView)
		es.POST("/:id/views/:vid/refresh", esHandler.RefreshView)
		// 分析（去重计数 / 图状）
		es.POST("/:id/analyze/distinct", esHandler.AnalyzeDistinct)
		es.POST("/:id/analyze/chart", esHandler.AnalyzeChart)
		// 生命周期（W1–W4）
		es.GET("/:id/ilm/policies", esHandler.ListILMPolicies)
		es.GET("/:id/ilm/policies/:name", esHandler.GetILMPolicy)
		es.PUT("/:id/ilm/policies/:name", esHandler.PutILMPolicy)
		es.DELETE("/:id/ilm/policies/:name", esHandler.DeleteILMPolicy)
		es.GET("/:id/ilm/explain", esHandler.ExplainILM)
		es.POST("/:id/ilm/retry", esHandler.RetryILM)
		es.GET("/:id/data-streams", esHandler.ListDataStreams)
		es.PUT("/:id/data-streams/:name/lifecycle", esHandler.PutDataStreamLifecycle)
		// 索引模板 / 组件模板（W5–W8 + 模拟）
		es.GET("/:id/index-templates", esHandler.ListIndexTemplates)
		es.GET("/:id/index-templates/:name", esHandler.GetIndexTemplate)
		es.PUT("/:id/index-templates/:name", esHandler.PutIndexTemplate)
		es.DELETE("/:id/index-templates/:name", esHandler.DeleteIndexTemplate)
		es.POST("/:id/index-templates/_simulate", esHandler.SimulateIndexTemplate)
		es.GET("/:id/component-templates", esHandler.ListComponentTemplates)
		es.PUT("/:id/component-templates/:name", esHandler.PutComponentTemplate)
		es.DELETE("/:id/component-templates/:name", esHandler.DeleteComponentTemplate)
	}

	// 工具-SFTP 连接、浏览与文件传输
	sftpRepo := repo.NewSFTPRepo()
	sftpHandler := sftpapi.NewHandler(sftpRepo, deps.CryptoService)
	sftp := protected.Group("/sftp")
	{
		sftp.GET("", sftpHandler.List)
		sftp.POST("", sftpHandler.Create)
		sftp.PUT("/:id", sftpHandler.Update)
		sftp.DELETE("/:id", sftpHandler.Delete)
		sftp.GET("/:id/ping", sftpHandler.Ping)
		sftp.GET("/:id/browse", sftpHandler.Browse)
		sftp.POST("/:id/mkdir", sftpHandler.Mkdir)
		sftp.POST("/:id/upload", sftpHandler.Upload)
		sftp.GET("/:id/download", sftpHandler.Download)
		sftp.DELETE("/:id/delete", sftpHandler.Remove)
		sftp.POST("/:id/rename", sftpHandler.Rename)
	}

	// 总览 & 审计日志（审计日志统一走 /api/sse/audits 单一查询流）
	miscHandler := overview.NewHandler(hostRepo, auditRepo)
	protected.GET("/overview", miscHandler.Overview)

	// SSE 推送
	sseHandler := sse.NewHandler(deps.Bus, hostRepo, credRepo, auditRepo)
	protected.GET("/sse/overview", sseHandler.Overview)
	protected.GET("/sse/hosts", sseHandler.HostStatus)
	protected.GET("/sse/audits", sseHandler.Audits)
	protected.GET("/sse/deploy", deployTaskHandler.SSEProgress)
	protected.GET("/sse/deploy/setup", deployTaskHandler.SSESetup)
	protected.GET("/sse/deploy/log", deployTaskHandler.SSELog)
	protected.GET("/sse/orchestration/steps", orchHandler.SSESteps)
	protected.GET("/sse/orchestration/detail", orchHandler.SSEDetail)
	protected.GET("/sse/stacks/setup", stackHandler.SSESetup)
	protected.GET("/sse/stacks/log", stackHandler.SSELog)

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
