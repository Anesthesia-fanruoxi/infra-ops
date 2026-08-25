// infra-ops 入口：打开DB→引导配置→装配路由→启动HTTP+巡检。
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/middleware"
	"infra-ops/common/netinfo"
	"infra-ops/common/probe"
	"infra-ops/common/sshx"
	"infra-ops/config"
	"infra-ops/router"
	"infra-ops/store"
	"infra-ops/template"
)

const (
	defaultAdminUser = "admin"
	defaultAdminPass = "admin123"
	dbPath           = "data/infra-ops.db"
)

func main() {
	// 打开数据库（路径固定，全部运行配置持久化于 settings 表）
	if err := store.Open(dbPath); err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	settingsRepo := store.NewSettingsRepo()

	// 首次启动自动生成主密钥与默认账号
	secretKey, err := icrypto.GenerateKey()
	if err != nil {
		log.Fatalf("生成主密钥失败: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPass), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("生成密码哈希失败: %v", err)
	}
	firstInit, err := settingsRepo.EnsureBootstrap(secretKey, string(hash))
	if err != nil {
		log.Fatalf("初始化默认配置失败: %v", err)
	}
	if firstInit {
		log.Printf("首次启动已完成初始化，默认账号 %s / %s，请登录后立即修改密码", defaultAdminUser, defaultAdminPass)
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
	hkRepo := store.NewHostKeyRepo()
	insecure := cfg.SSH.HostKeyPolicy == "insecure"
	sshClient := sshx.NewClient(cfg.SSH.Timeout, hkRepo, insecure)

	// 初始化会话存储（无状态签名，重启不失效）
	sessions := middleware.NewSessionStore(cfg.Security.SecretKey)

	// 初始化事件总线
	bus := eventbus.New()

	// 启动巡检协程
	hostRepo := store.NewHostRepo()
	credRepo := store.NewCredentialRepo()
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

	// 装配路由
	r := router.Setup(template.FS, router.Deps{
		CryptoService:     cryptoSvc,
		SSHClient:         sshClient,
		Sessions:          sessions,
		Bus:               bus,
		Settings:          settingsRepo,
		DeployConcurrency: cfg.Deploy.Concurrency,
	})

	// 部署日志保留清理：启动时立即执行一次，此后每 24h 一轮
	go runRetentionLoop(settingsRepo)

	// 启动 HTTP 服务
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logNetworkInfo()
	log.Printf("infra-ops starting on %s", addr)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// logNetworkInfo 输出本机内网 IP 与公网出口 IP（如有）。
func logNetworkInfo() {
	local := netinfo.LocalIPv4s()
	if len(local) > 0 {
		log.Printf("[net] 内网IP: %s", strings.Join(local, ", "))
	}
	ch := make(chan string, 1)
	go func() { ch <- netinfo.PublicIP() }()
	select {
	case pub := <-ch:
		if pub != "" {
			log.Printf("[net] 公网IP: %s", pub)
		}
	case <-time.After(5 * time.Second):
		log.Printf("[net] 公网IP探测超时，已跳过")
	}
}

// runRetentionLoop 周期清理超过保留期的部署任务及其日志（外键级联删除主机记录）。
func runRetentionLoop(settingsRepo *store.SettingsRepo) {
	run := func() {
		days, err := strconv.Atoi(mustSetting(settingsRepo, store.SettingLogRetentionDays))
		if err != nil || days <= 0 {
			return
		}
		n, err := store.NewDeployRepo().CleanupFinishedBefore(days)
		if err != nil {
			log.Printf("[retention] 清理部署历史失败: %v", err)
			return
		}
		if n > 0 {
			log.Printf("[retention] 已清理 %d 天前的部署任务 %d 个", days, n)
		}
	}

	run()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}

func mustSetting(r *store.SettingsRepo, key string) string {
	v, _ := r.Get(key)
	return v
}
