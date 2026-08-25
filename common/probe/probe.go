// Package probe 心跳巡检：周期 SSH 连通性+资源采集。
package probe

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/sshx"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store"
)

// Probe 巡检服务。
type Probe struct {
	hostRepo    *store.HostRepo
	credRepo    *store.CredentialRepo
	cryptoS     *crypto.Service
	sshC        *sshx.Client
	bus         *eventbus.Bus
	intervalSec int
	concurrency int
	stopCh      chan struct{}
}

// Deps 巡检依赖。
type Deps struct {
	HostRepo    *store.HostRepo
	CredRepo    *store.CredentialRepo
	CryptoS     *crypto.Service
	SSHC        *sshx.Client
	Bus         *eventbus.Bus
	IntervalSec int
	Concurrency int
}

// New 创建巡检服务。
func New(deps Deps) *Probe {
	return &Probe{
		hostRepo:    deps.HostRepo,
		credRepo:    deps.CredRepo,
		cryptoS:     deps.CryptoS,
		sshC:        deps.SSHC,
		bus:         deps.Bus,
		intervalSec: deps.IntervalSec,
		concurrency: deps.Concurrency,
		stopCh:      make(chan struct{}),
	}
}

// Start 启动巡检协程。
func (p *Probe) Start() {
	go p.loop()
	if p.concurrency <= 0 {
		log.Printf("probe started: interval=%ds concurrency=auto", p.intervalSec)
	} else {
		log.Printf("probe started: interval=%ds concurrency=%d", p.intervalSec, p.concurrency)
	}
}

// Stop 停止巡检。
func (p *Probe) Stop() {
	close(p.stopCh)
}

func (p *Probe) loop() {
	p.runOnce()
	ticker := time.NewTicker(time.Duration(p.intervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.runOnce()
		}
	}
}

func (p *Probe) runOnce() {
	hosts, err := p.hostRepo.ListAll()
	if err != nil {
		log.Printf("probe: list hosts: %v", err)
		return
	}

	concurrency := p.concurrency
	if concurrency <= 0 {
		concurrency = sysutil.AdaptiveConcurrency(len(hosts)) // 每轮按当前主机数自适应
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i := range hosts {
		wg.Add(1)
		sem <- struct{}{}
		go func(h model.Host) {
			defer wg.Done()
			defer func() { <-sem }()
			p.probeHost(&h)
		}(hosts[i])
	}
	wg.Wait()
}

func (p *Probe) probeHost(h *model.Host) {
	cred, err := p.credRepo.GetByID(h.CredentialID)
	if err != nil || cred == nil {
		p.markOffline(h.ID)
		return
	}

	secret, err := p.cryptoS.Decrypt(cred.EncryptedSecret)
	if err != nil {
		log.Printf("probe: decrypt cred %d: %v", cred.ID, err)
		p.markOffline(h.ID)
		return
	}

	addr := fmt.Sprintf("%s:%d", h.IP, h.Port)
	dialCfg := sshx.DialConfig{Addr: addr, Username: cred.Username}
	switch cred.Type {
	case "private_key":
		dialCfg.PrivateKey = secret
	case "password":
		dialCfg.Password = string(secret)
	}

	client, err := p.sshC.Dial(dialCfg)
	if err != nil {
		log.Printf("probe: dial %s: %v", addr, err)
		p.markOffline(h.ID)
		return
	}
	defer client.Close()

	result, err := sshx.Collect(client)
	if err != nil {
		log.Printf("probe: collect %s: %v", addr, err)
		p.markOffline(h.ID)
		return
	}

	infoJSON, _ := json.Marshal(result.Info)
	if err := p.hostRepo.UpdateProbeResult(h.ID, "online", int(result.LatencyMs), string(infoJSON)); err != nil {
		log.Printf("probe: update %s: %v", addr, err)
	}
	// 台账名自动跟随系统主机名（重名冲突时跳过，保留原名）
	if hn := strings.TrimSpace(result.Info.Hostname); hn != "" && hn != h.Name {
		if err := p.hostRepo.Rename(h.ID, hn); err != nil {
			log.Printf("probe: 同步主机名 %s -> %s: %v", h.Name, hn, err)
		} else {
			h.Name = hn
		}
	}
	// 发布状态更新事件
	if p.bus != nil {
		p.bus.Publish("host.status", map[string]interface{}{
			"id":     h.ID,
			"status": "online",
			"latency_ms": result.LatencyMs,
			"info_json": string(infoJSON),
		})
	}
}

func (p *Probe) markOffline(hostID int64) {
	p.hostRepo.UpdateProbeResult(hostID, "offline", 0, "{}")
	// 发布状态更新事件
	if p.bus != nil {
		p.bus.Publish("host.status", map[string]interface{}{
			"id":     hostID,
			"status": "offline",
			"latency_ms": 0,
			"info_json": "{}",
		})
	}
}
