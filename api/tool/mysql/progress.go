package mysql

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
)

// 导出 / 导入这类长任务的实时进度。
//
// 为什么不走 SSE：项目的 SSE 是「订阅广播」模型（主机状态、审计日志），
// 而这里要的是「一次请求一个流」的按需进度。用注册表 + 轮询更直接，
// 也不会在客户端断开后留下无人消费的广播。请求本身仍是同步的，
// 进度只是旁路信息——拿到就显示，拿不到（被淘汰）也不影响结果。

// 进度条目保留多久、最多留多少条：都是防内存泄漏用的，与业务无关。
const (
	transferKeepAlive = 30 * time.Minute
	transferMaxItems  = 128
)

// TransferStatus 一次导出 / 导入的当前状态（下发给前端）。
type TransferStatus struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`    // export / import
	Phase      string `json:"phase"`   // running / done / error
	Rows       int64  `json:"rows"`    // 已处理行数（导出：写出；导入：影响）
	Statements int64  `json:"statements"`
	Batches    int64  `json:"batches"`
	Bytes      int64  `json:"bytes"`
	Message    string `json:"message"`
	ElapsedMs  int64  `json:"elapsed_ms"`
}

type transferState struct {
	TransferStatus
	started time.Time
	updated time.Time
}

type transferProgress struct {
	mu sync.Mutex
	m  map[string]*transferState
}

func newTransferProgress() *transferProgress {
	return &transferProgress{m: map[string]*transferState{}}
}

// begin 登记一个新任务。key 为空时返回 nil（调用方所有方法都变成空操作）。
func (p *transferProgress) begin(key, kind string) *transferHandle {
	if p == nil || key == "" {
		return nil
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked(now)
	p.m[key] = &transferState{
		TransferStatus: TransferStatus{Key: key, Kind: kind, Phase: "running"},
		started:        now,
		updated:        now,
	}
	return &transferHandle{p: p, key: key}
}

// get 取某任务的当前状态；已被淘汰或从未登记则返回 false。
func (p *transferProgress) get(key string) (TransferStatus, bool) {
	if p == nil || key == "" {
		return TransferStatus{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.m[key]
	if !ok {
		return TransferStatus{}, false
	}
	out := st.TransferStatus
	out.ElapsedMs = time.Since(st.started).Milliseconds()
	return out, true
}

// evictLocked 清掉过期条目；仍然超量时按最后更新时间淘汰最旧的。
func (p *transferProgress) evictLocked(now time.Time) {
	for k, st := range p.m {
		if now.Sub(st.updated) > transferKeepAlive {
			delete(p.m, k)
		}
	}
	for len(p.m) >= transferMaxItems {
		oldestKey, oldestAt := "", now
		for k, st := range p.m {
			if oldestKey == "" || st.updated.Before(oldestAt) {
				oldestKey, oldestAt = k, st.updated
			}
		}
		delete(p.m, oldestKey)
	}
}

// transferHandle 任务进度的写入口；nil 安全。
type transferHandle struct {
	p   *transferProgress
	key string
}

func (h *transferHandle) update(fn func(*TransferStatus)) {
	if h == nil || h.p == nil {
		return
	}
	h.p.mu.Lock()
	defer h.p.mu.Unlock()
	st, ok := h.p.m[h.key]
	if !ok {
		return
	}
	fn(&st.TransferStatus)
	st.updated = time.Now()
}

// Running 记录当前计数（由调用方按自己的节拍调用，不必每行都调）。
func (h *transferHandle) Running(rows, statements, batches, bytes int64) {
	h.update(func(s *TransferStatus) {
		s.Rows, s.Statements, s.Batches, s.Bytes = rows, statements, batches, bytes
	})
}

// Note 附一句当前动作说明（导入时用来说「正在执行第 N 条」）。
func (h *transferHandle) Note(msg string) {
	h.update(func(s *TransferStatus) { s.Message = msg })
}

// Finish 收尾：phase 落 done，err 非空则落 error。
func (h *transferHandle) Finish(err error) {
	h.update(func(s *TransferStatus) {
		if err != nil {
			s.Phase = "error"
			s.Message = err.Error()
			return
		}
		s.Phase = "done"
	})
}

// TransferProgress GET /api/mysql/:id/transfer/progress?key=
//
// 进度是旁路信息：请求本身同步返回，这里只是让前端在等待期间能看到进展。
// 查不到（已结束很久、条目被淘汰）就如实回 found=false，前端据此停止轮询。
func (h *Handler) TransferProgress(c *gin.Context) {
	key := strings.TrimSpace(c.Query("key"))
	st, ok := h.transfers.get(key)
	if !ok {
		resp.OK(c, gin.H{"found": false, "active": false})
		return
	}
	resp.OK(c, gin.H{"found": true, "active": st.Phase == "running", "status": st})
}
