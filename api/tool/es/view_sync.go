// 数据视图后台同步：启动延迟 10s 跑一次 + 每 4h 轮询。
// 并发模型：并行拉取（上限 4）→ 内存缓存 → 单事务批量入库（只写成功视图）。
// 单视图互斥（进程内 sync.Map），失败保留旧快照，启动重置残留 syncing（自愈）。
package es

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"infra-ops/common/crypto"
	"infra-ops/model"
	"infra-ops/store/repo"
)

// 同步参数（§6.2 / §6.3）。
const (
	esSyncStartupDelay = 10 * time.Second
	esSyncInterval     = 4 * time.Hour
	esSyncConcurrency  = 4 // 并行拉取信号量上限
)

// syncLock 单视图互斥标记，覆盖「拉取 + 入库」全过程。
// 手动刷新与后台轮次共用，撞上即在途 → 4008。
type syncLock struct {
	mu sync.Map // map[int64]struct{}
}

// tryAcquire 尝试占位；已在同步中返回 false。
func (l *syncLock) tryAcquire(viewID int64) bool {
	_, loaded := l.mu.LoadOrStore(viewID, struct{}{})
	return !loaded
}

func (l *syncLock) release(viewID int64) {
	l.mu.Delete(viewID)
}

// viewSyncManager 后台同步的编排器，持有加密与两个仓储。
type viewSyncManager struct {
	crypto   *crypto.Service
	esRepo   *repo.ESRepo
	viewRepo *repo.ESViewRepo
	lock     *syncLock
}

// NewViewSyncManager 构造后台同步管理器。
func NewViewSyncManager(cs *crypto.Service, esRepo *repo.ESRepo, vr *repo.ESViewRepo) *viewSyncManager {
	return &viewSyncManager{crypto: cs, esRepo: esRepo, viewRepo: vr, lock: &syncLock{}}
}

// Run 以后台循环运行：启动延迟 10s 一次，此后每 4h 一轮；ticker 串行发起，轮次不重叠。
func (m *viewSyncManager) Run(ctx context.Context) {
	time.Sleep(esSyncStartupDelay)
	m.round(ctx)
	ticker := time.NewTicker(esSyncInterval)
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

// round 一轮全量同步：遍历全部连接，逐连接内并行拉取各视图，再单事务批量入库。
func (m *viewSyncManager) round(ctx context.Context) {
	// 启动先重置残留 syncing（进程崩溃自愈）
	if err := m.viewRepo.ResetSyncingAll(); err != nil {
		log.Printf("[es-sync] 重置残留 syncing 失败: %v", err)
	}
	conns, err := m.esRepo.ListAll()
	if err != nil {
		log.Printf("[es-sync] 读取连接列表失败: %v", err)
		return
	}
	start := time.Now()
	total := 0
	for i := range conns {
		n := m.syncConn(ctx, &conns[i])
		total += n
	}
	log.Printf("[es-sync] 全量同步完成：%d 个连接 / %d 个视图，耗时 %s", len(conns), total, time.Since(start).Round(time.Millisecond))
}

// syncConn 同步单个连接下的全部视图。连接级失败不扩散：解析失败则该连接下所有视图标失败。
func (m *viewSyncManager) syncConn(ctx context.Context, conn *model.ESConn) int {
	views, err := m.viewRepo.ListByConn(conn.ID)
	if err != nil {
		log.Printf("[es-sync] 连接 #%d 读取视图失败: %v", conn.ID, err)
		return 0
	}
	if len(views) == 0 {
		return 0
	}
	client, code, msg := m.resolveConn(conn)
	if client == nil {
		// 连接不可达 / 凭据失效 → 该连接下全部视图标记失败，其它连接不受影响
		updates := make([]repo.SyncUpdate, 0, len(views))
		for i := range views {
			updates = append(updates, repo.SyncUpdate{ViewID: views[i].ID, ErrMsg: msg})
		}
		if err := m.viewRepo.SaveSyncResults(updates); err != nil {
			log.Printf("[es-sync] 连接 #%d 失败态入库失败: %v", conn.ID, err)
		}
		log.Printf("[es-sync] 连接 #%d 不可达（%s），该连接 %d 个视图标失败", conn.ID, msg, len(views))
		return len(views)
	}
	_ = code

	// 并行拉取，信号量限制并发
	sem := make(chan struct{}, esSyncConcurrency)
	results := make([]repo.SyncUpdate, len(views))
	var wg sync.WaitGroup
	for i := range views {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v := &views[idx]
			results[idx] = m.pullOne(ctx, client, v)
		}(i)
	}
	wg.Wait()

	// 单事务批量入库，只写成功的视图
	if err := m.viewRepo.SaveSyncResults(results); err != nil {
		// 整轮失败整体丢弃，库内保持上一份成功快照；不做逐视图补偿
		log.Printf("[es-sync] 连接 #%d 批量入库失败（本轮结果丢弃）: %v", conn.ID, err)
		return len(views)
	}
	return len(views)
}

// pullOne 单视图同步：占互斥 → 拉取合并 → 组装入库载荷。
func (m *viewSyncManager) pullOne(ctx context.Context, client *escClient, v *model.ESView) repo.SyncUpdate {
	viewID := v.ID
	if !m.lock.tryAcquire(viewID) {
		return repo.SyncUpdate{ViewID: viewID, ErrMsg: "视图正在同步中，已跳过"}
	}
	defer m.lock.release(viewID)

	if err := m.viewRepo.MarkSyncing(viewID); err != nil {
		return repo.SyncUpdate{ViewID: viewID, ErrMsg: "标记同步中失败: " + err.Error()}
	}

	cc, err := collateView(ctx, client, v.IndexPattern)
	if err != nil {
		return repo.SyncUpdate{ViewID: viewID, ErrMsg: err.Error()}
	}
	// 时间字段若已失效则回退到候选首个，不阻断整视图同步
	timeField := v.TimeField
	if tf, verr := resolveTimeField(cc, v.TimeField); verr == nil {
		timeField = tf
	}
	if timeField != v.TimeField {
		m.viewRepo.UpdateBasic(viewID, v.Name, v.IndexPattern, timeField)
	}
	fieldsJSON, _ := json.Marshal(cc.Fields)
	stats := statsSnapshot{Indices: cc.Indices, Aliases: cc.Aliases, Streams: cc.Streams,
		DocsCount: cc.DocsCount, Truncated: cc.Truncated}
	statsJSON, _ := json.Marshal(stats)
	return repo.SyncUpdate{
		ViewID: viewID, FieldsJSON: string(fieldsJSON), StatsJSON: string(statsJSON),
	}
}

// resolveConn 从连接配置解密密码并构造客户端。
func (m *viewSyncManager) resolveConn(conn *model.ESConn) (*escClient, int, string) {
	var password []byte
	if conn.AuthType == "basic" && len(conn.EncryptedSecret) > 0 {
		pw, err := m.crypto.Decrypt(conn.EncryptedSecret)
		if err != nil {
			return nil, 0, "解密连接密码失败: " + err.Error()
		}
		password = pw
	}
	client, err := newESCClient(conn.URL, conn.Insecure, conn.AuthType, conn.Username, string(password))
	if err != nil {
		return nil, 0, "连接地址无效: " + err.Error()
	}
	return client, 0, ""
}
