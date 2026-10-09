// infra-ops 入口：桌面应用（Wails v3）。
// Gin 引擎直接作为桌面窗口的资产服务器 handler（进程内直连，不监听任何端口），
// 页面与 /api 请求同源，由原生窗口（Windows 为 WebView2）渲染与访问。
package main

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	esapi "infra-ops/api/tool/es"
	sftpapi "infra-ops/api/tool/sftp"
	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/probe"
	"infra-ops/common/sshx"
	"infra-ops/config"
	"infra-ops/desktop"
	"infra-ops/router"
	"infra-ops/store"
	"infra-ops/store/repo"
	"infra-ops/store/setting"
	"infra-ops/template"
)

const (
	dbPath     = "data/infra-ops.db"
	appDirName = "infra-ops"
)

func main() {
	// 桌面模式工作目录固定为用户数据目录（Windows: %APPDATA%\infra-ops），
	// 使全部相对路径（data/infra-ops.db、data/assets/...）落在用户目录下。
	if err := switchToUserDataDir(); err != nil {
		log.Fatalf("初始化数据目录失败: %v", err)
	}
	// 桌面构建带 -H windowsgui，没有可见控制台，log 默认输出无处可去——
	// 后端 panic 与内部错误就等于凭空消失（前端只能显示一句 500）。
	// 故统一重定向到 data/app.log，同时保留文件与标准错误两路。
	if err := redirectLog("data/app.log"); err != nil {
		// 日志文件开不了不该拦住启动：退回默认输出，至少 stderr 还有人在看
		log.Printf("打开日志文件失败（继续用默认输出）: %v", err)
	}

	// 打开数据库（路径相对用户数据目录，全部运行配置持久化于 settings 表）
	if err := store.Open(dbPath); err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	settingsRepo := setting.NewSettingsRepo()

	// 首次启动自动生成主密钥
	secretKey, err := icrypto.GenerateKey()
	if err != nil {
		log.Fatalf("生成主密钥失败: %v", err)
	}
	firstInit, err := settingsRepo.EnsureBootstrap(secretKey)
	if err != nil {
		log.Fatalf("初始化默认配置失败: %v", err)
	}
	if err := settingsRepo.EnsureRuntimeDefaults(); err != nil {
		log.Fatalf("补齐运行配置失败: %v", err)
	}

	// 从 settings 构建运行配置
	m, err := settingsRepo.GetAll()
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	cfg := config.FromSettings(m)

	// 初始化加解密服务
	cryptoSvc, err := icrypto.NewService(cfg.Security.SecretKey)
	if err != nil {
		log.Fatalf("初始化加密服务失败: %v", err)
	}

	// 初始化 SSH 通道
	hkRepo := repo.NewHostKeyRepo()
	insecure := cfg.SSH.HostKeyPolicy == "insecure"
	sshClient := sshx.NewClient(cfg.SSH.Timeout, hkRepo, insecure)

	// 初始化事件总线
	bus := eventbus.New()

	// 启动巡检协程
	hostRepo := repo.NewHostRepo()
	credRepo := repo.NewCredentialRepo()
	probeSvc := probe.New(probe.Deps{
		HostRepo:    hostRepo,
		CredRepo:    credRepo,
		CryptoS:     cryptoSvc,
		SSHC:        sshClient,
		Bus:         bus,
		IntervalSec: cfg.Probe.Interval,
		Concurrency: cfg.Probe.Concurrency,
	})
	probeSvc.Start()
	defer probeSvc.Stop()

	// 装配路由（引擎交给桌面窗口的资产服务器，替代原 HTTP 监听）
	engine := router.Setup(template.FS, router.Deps{
		CryptoService:     cryptoSvc,
		SSHClient:         sshClient,
		Bus:               bus,
		Settings:          settingsRepo,
		DeployConcurrency: cfg.Deploy.Concurrency,
	})

	// 部署日志保留清理：启动时立即执行一次，此后每 24h 一轮
	go runRetentionLoop(settingsRepo)

	// ES 数据视图异步同步：延迟 10s 先跑一次，此后每 4h 一轮
	go esapi.NewViewSyncManager(cryptoSvc, repo.NewESRepo(), repo.NewESViewRepo()).Run(context.Background())

	// 创建桌面应用与主窗口
	winOpts := application.WindowsOptions{}
	if hasArg("-cdp") {
		// -cdp：开启 WebView2 远程调试端口（仅限本机），供自动化测试/排障使用
		winOpts.AdditionalBrowserArgs = []string{"--remote-debugging-port=9223"}
	}
	// 单实例：重复启动时第二实例向首实例发送通知后自动退出，
	// 首实例收到回调后还原并置前已有窗口。
	var window *application.WebviewWindow
	app := application.New(application.Options{
		Name:        "infra-ops",
		Description: "基建运维平台：服务器接入、巡检、初始化、中间件安装",
		Assets: application.AssetOptions{
			Handler: engine,
		},
		Windows: winOpts,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "infra-ops.desktop",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				log.Printf("检测到重复启动，已激活当前窗口（参数: %v）", data.Args)
				if window != nil {
					window.Restore()
					window.Focus()
				}
			},
		},
	})
	// 桌面桥服务：SSE 事件桥 + 文件服务（原生对话框与直传/直落盘）
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "infra-ops",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 680,
		URL:       "/",
	})
	window.Center()
	app.RegisterService(application.NewService(desktop.NewSSEBridge(app, engine)))
	app.RegisterService(application.NewService(desktop.NewFileService(app, window, sftpapi.NewHandler(repo.NewSFTPRepo(), cryptoSvc))))
	// F12 开 DevTools：Wails 关掉了浏览器加速键且 Windows 不派发 key binding 事件，
	// 只能由前端 keydown → Call.ByName 走这条桥。详见 desktop/devtools.go 的三重限制说明。
	app.RegisterService(application.NewService(desktop.NewDevToolsService(window)))

	// -debug 启动参数：启动即打开 DevTools，便于排查。
	// 注意本构建必须带 devtools 标签（见 script/build-desktop.ps1），否则 Wails 的
	// OpenDevTools 是空函数，这里调了也什么都不发生 —— 故先判能力再决定要不要提示。
	if hasArg("-debug") {
		if desktop.DevToolsCompiled {
			window.OpenDevTools()
		} else {
			log.Printf("-debug 需要 devtools 构建标签，本构建不含 DevTools 能力，已忽略（构建见 script/build-desktop.ps1）")
		}
	}

	// 首次启动提示（桌面模式无可见控制台）
	if firstInit {
		log.Printf("首次启动已完成初始化，可直接使用")
	}

	if err := app.Run(); err != nil {
		log.Fatalf("桌面应用运行失败: %v", err)
	}
}

// switchToUserDataDir 将进程工作目录切换到用户数据目录并确保其存在。
// Windows: %APPDATA%\infra-ops；macOS: ~/Library/Application Support/infra-ops；Linux: ~/.config/infra-ops
func switchToUserDataDir() error {
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Chdir(dir)
}

// redirectLog 把标准库 log 同时输出到文件与标准错误，并打上时间戳。
// 桌面模式无控制台，日志文件是后端侧唯一的排障通道（前端错误提示里也会指到这里）。
func redirectLog(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	return nil
}

// hasArg 判断启动参数中是否存在指定项（如 -debug）。
func hasArg(name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}

// runRetentionLoop 周期清理超过保留期的部署任务及其日志（外键级联删除主机记录）。
func runRetentionLoop(settingsRepo *setting.SettingsRepo) {
	run := func() {
		days, err := strconv.Atoi(mustSetting(settingsRepo, setting.SettingLogRetentionDays))
		if err != nil || days <= 0 {
			return
		}
		n, err := repo.NewDeployRepo().CleanupFinishedBefore(days)
		if err != nil {
			log.Printf("[retention] 清理部署历史失败: %v", err)
			return
		}
		if n > 0 {
			log.Printf("[retention] 已清理 %d 天前的部署任务 %d 个", days, n)
		}
		m, err := repo.NewOrchestrationRepo().CleanupRunsBefore(days)
		if err != nil {
			log.Printf("[retention] 清理任务记录失败: %v", err)
			return
		}
		if m > 0 {
			log.Printf("[retention] 已清理 %d 天前的任务记录 %d 条", days, m)
		}
		// MySQL 工具的使用记录（SQL 执行记录 + AI 调用记录）与上面同一保留期
		aiN, sqlN, err := repo.NewMySQLLogRepo().PurgeBefore(days)
		if err != nil {
			log.Printf("[retention] 清理 MySQL 使用记录失败: %v", err)
			return
		}
		if aiN > 0 || sqlN > 0 {
			log.Printf("[retention] 已清理 %d 天前的 MySQL 记录：AI 调用 %d 条、SQL 执行 %d 条", days, aiN, sqlN)
		}
	}

	run()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}

func mustSetting(r *setting.SettingsRepo, key string) string {
	v, _ := r.Get(key)
	return v
}
