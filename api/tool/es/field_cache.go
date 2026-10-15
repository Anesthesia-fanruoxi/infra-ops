// field_cache.go ES「系统自带字段」的后台探测与缓存。
//
// 为什么放后台跑、而不是生成配置时现查：探测要打集群（版本 + 组件模板 + field_caps 三个请求），
// 而生成 logstash 配置本来是个纯本地的拼装动作。让它依赖集群在线、每次多等三个 RTT 不划算，
// 而这份清单变化很慢（集群升级、或上了新采集器才会变），所以启动后探测一次、之后按周期刷新。
//
// 与视图同步（view_sync.go）的区别：那边只写成功的快照，这里**失败也要落库**
// （status=failed + 原因）—— 界面得能回答「为什么这份清单是空的」。
package es

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"infra-ops/common/crypto"
	"infra-ops/model"
	"infra-ops/store/repo"
)

const (
	// 错开视图同步的 10s：两者都要打集群，同时起会把启动瞬间的请求叠在一起
	esFieldProbeStartupDelay = 15 * time.Second
	esFieldProbeInterval     = 24 * time.Hour
	// esFieldProbeTimeout 单次探测的超时。异步触发（新建 / 更新连接）时靠它兜住，
	// 免得集群半死不活把一个 goroutine 永远挂在那里。
	esFieldProbeTimeout = 20 * time.Second
)

// fieldCacheManager 后台探测的编排器。
type fieldCacheManager struct {
	crypto    *crypto.Service
	esRepo    *repo.ESRepo
	fieldRepo *repo.ESFieldRepo
	// lock 让「手动刷新」与「后台轮次」串行：探测本身是只读的，但两路同时打同一集群没意义
	lock sync.Mutex
}

// NewFieldCacheManager 构造后台探测管理器。
func NewFieldCacheManager(cs *crypto.Service, esRepo *repo.ESRepo, fr *repo.ESFieldRepo) *fieldCacheManager {
	return &fieldCacheManager{crypto: cs, esRepo: esRepo, fieldRepo: fr}
}

// Run 以后台循环运行：启动延迟 15s 一次，此后每 24h 一轮。
func (m *fieldCacheManager) Run(ctx context.Context) {
	time.Sleep(esFieldProbeStartupDelay)
	m.round(ctx)
	ticker := time.NewTicker(esFieldProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.round(ctx)
		}
	}
}

// round 一轮全量：遍历全部连接逐个探测。连接级失败不扩散，只记日志。
func (m *fieldCacheManager) round(ctx context.Context) {
	conns, err := m.esRepo.ListAll()
	if err != nil {
		log.Printf("[es-field] 读取连接列表失败: %v", err)
		return
	}
	if len(conns) == 0 {
		return
	}
	start := time.Now()
	failed := 0
	for i := range conns {
		if err := m.ProbeConn(ctx, &conns[i]); err != nil {
			failed++
			log.Printf("[es-field] 连接 #%d 探测失败: %v", conns[i].ID, err)
		}
	}
	log.Printf("[es-field] 字段探测完成：%d 个连接（失败 %d），耗时 %s",
		len(conns), failed, time.Since(start).Round(time.Millisecond))
}

// ProbeConn 探测单个连接并落库 —— 后台轮次、新建连接、手动刷新三个入口共用这一个。
//
// 连「解密失败 / 地址无效」这类前置错误也落库（status=failed + 原因）：
// 界面要能区分「还没探测过」与「探测了但失败了」，否则用户只看到一个空清单。
func (m *fieldCacheManager) ProbeConn(ctx context.Context, conn *model.ESConn) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	return probeAndSave(ctx, m.crypto, m.fieldRepo, conn)
}

// probeAndSave 探测一次并落库。后台轮次与「新建连接后立刻探一次」共用这一份逻辑。
//
// 连「解密失败 / 地址无效」这类前置错误也落库（status=failed + 原因）：
// 界面要能区分「还没探测过」与「探测了但失败了」，否则用户只看到一个空清单。
func probeAndSave(ctx context.Context, cs *crypto.Service, fr *repo.ESFieldRepo, conn *model.ESConn) error {
	client, err := resolveESClient(cs, conn)
	if err != nil {
		_ = fr.SaveProbe(conn.ID, "", []string{}, esFieldStatusFailed, err.Error())
		return err
	}
	version, fields, status, msg := probeESDefaultFields(ctx, client)
	return fr.SaveProbe(conn.ID, version, fields, status, msg)
}

// resolveESClient 解密连接密码并构造客户端。
// 视图同步（view_sync.go）与字段探测共用 —— 两处都要「解密码 + 建客户端」，
// 分开写迟早会有一边漏掉 insecure 或 authType 的处理。
func resolveESClient(cs *crypto.Service, conn *model.ESConn) (*escClient, error) {
	var password []byte
	if conn.AuthType == "basic" && len(conn.EncryptedSecret) > 0 {
		pw, err := cs.Decrypt(conn.EncryptedSecret)
		if err != nil {
			return nil, fmt.Errorf("解密连接密码失败: %w", err)
		}
		password = pw
	}
	client, err := newESCClient(conn.URL, conn.Insecure, conn.AuthType, conn.Username, string(password))
	if err != nil {
		return nil, fmt.Errorf("连接地址无效: %w", err)
	}
	return client, nil
}

// systemFields 取这个连接要删的「系统自带字段」：缓存里的自动清单 + 人工补充。
//
// 三种情况下退回候选池，**不给空清单**：缓存没探测过、探测失败、探测结果为空
// （集群里没采到任何元数据）。空清单意味着「一个字段都不删」，而用户开这个开关的意图
// 就是「把采集器塞的那些清掉」—— 退回候选池至少覆盖常见元数据字段，界面会用
// cache_status 说明清单的来源，用户能判断要不要手工补。
func (h *Handler) systemFields(connID int64) (fields []string, status, version string) {
	fallback := func() []string { return append([]string{}, esMetaCandidateTop...) }
	if h.fieldRepo == nil {
		return fallback(), "", ""
	}
	c, err := h.fieldRepo.Get(connID)
	if err != nil || c == nil {
		return fallback(), "", ""
	}
	if c.Status == esFieldStatusFailed {
		return fallback(), c.Status, c.ESVersion
	}
	out := make([]string, 0, len(c.Fields)+len(c.Extra))
	out = append(out, c.Fields...)
	out = append(out, c.Extra...)
	if len(out) == 0 {
		out = fallback()
	}
	return out, c.Status, c.ESVersion
}
