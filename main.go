// infra-ops 入口：加载配置→初始化DB→装配路由→启动HTTP+巡检。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/middleware"
	"infra-ops/common/probe"
	"infra-ops/common/sshx"
	"infra-ops/config"
	"infra-ops/router"
	"infra-ops/store"
	"infra-ops/template"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化数据库
	if err := store.Open(cfg.Database.Path); err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	// 初始化加解密服务
	cryptoSvc, err := icrypto.NewService(cfg.Security.SecretKey)
	if err != nil {
		log.Fatalf("初始化加密服务失败: %v（请用 infra-ops keygen 生成主密钥）", err)
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
		Cfg:           cfg,
		CryptoService: cryptoSvc,
		SSHClient:     sshClient,
		Sessions:      sessions,
		Bus:           bus,
	})

	// 启动 HTTP 服务
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("infra-ops starting on %s", addr)
	if err := r.Run(addr); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
