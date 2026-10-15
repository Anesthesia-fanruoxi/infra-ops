// import_progress.go 导入的实时进度（注册表 + 轮询）。
//
// 为什么不走 SSE：与 mysql/progress.go 同一个理由 —— 项目的 SSE 是「订阅广播」模型
// （主机状态、审计日志），而这里要的是「一次请求一个流」的按需进度。
// 导入请求本身仍是同步的，进度只是旁路信息：取得到就显示，取不到也不影响结果。
package es

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
)

// 保留多久、最多留几份：都是防内存泄漏的，与业务无关。
const (
	importKeepAlive = 30 * time.Minute
	importMaxItems  = 64
)

// ImportStatus 一次导入的当前状态（下发给前端轮询）。
type ImportStatus struct {
	Key       string `json:"key"`
	Phase     string `json:"phase"` // running / done / error
	Read      int64  `json:"read"`  // 已读出的记录数
	Indexed   int64  `json:"indexed"`
	Failed    int64  `json:"failed"`  // bulk 写入被拒
	Skipped   int64  `json:"skipped"` // 未匹配 pattern / 坏数据
	Batches   int64  `json:"batches"`
	Bytes     int64  `json:"bytes"`
	Origin    string `json:"origin"` // 当前处理到哪个文件（压缩包内）
	Message   string `json:"message"`
	ElapsedMs int64  `json:"elapsed_ms"`
	// Waiting 跳过超阈值时挂起（等前端决策：继续导入 / 中止转重新生成）；
	// SkipSamples 是挂起现场收集的跳过样例（供「重新生成」合并进提示词）。
	Waiting     bool     `json:"waiting"`
	SkipSamples []string `json:"skip_samples,omitempty"`
	// Files/FileStep 多文件（或压缩包多条目）时的逐步进度：
	// Files 是处理清单，FileStep 是正在读的序号（1-based，0=未开始）——前端抽屉逐文件打勾/转圈。
	Files    []string `json:"files,omitempty"`
	FileStep int      `json:"file_step,omitempty"`
}

type importState struct {
	ImportStatus
	started time.Time
	updated time.Time
}

type importProgress struct {
	mu sync.Mutex
	m  map[string]*importState
}

func newImportProgress() *importProgress {
	return &importProgress{m: map[string]*importState{}}
}

// begin 登记一次导入；key 为空时返回 nil（其所有方法都变成空操作）。
func (p *importProgress) begin(key string) *importHandle {
	if p == nil || key == "" {
		return nil
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked(now)
	p.m[key] = &importState{
		ImportStatus: ImportStatus{Key: key, Phase: "running"},
		started:      now,
		updated:      now,
	}
	return &importHandle{p: p, key: key}
}

func (p *importProgress) get(key string) (ImportStatus, bool) {
	if p == nil || key == "" {
		return ImportStatus{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.m[key]
	if !ok {
		return ImportStatus{}, false
	}
	out := st.ImportStatus
	out.ElapsedMs = time.Since(st.started).Milliseconds()
	return out, true
}

func (p *importProgress) evictLocked(now time.Time) {
	for k, st := range p.m {
		if now.Sub(st.updated) > importKeepAlive {
			delete(p.m, k)
		}
	}
	for len(p.m) >= importMaxItems {
		oldestKey, oldestAt := "", now
		for k, st := range p.m {
			if oldestKey == "" || st.updated.Before(oldestAt) {
				oldestKey, oldestAt = k, st.updated
			}
		}
		delete(p.m, oldestKey)
	}
}

// importHandle 进度的写入口；nil 安全。
type importHandle struct {
	p   *importProgress
	key string
}

func (h *importHandle) update(fn func(*ImportStatus)) {
	if h == nil || h.p == nil {
		return
	}
	h.p.mu.Lock()
	defer h.p.mu.Unlock()
	st, ok := h.p.m[h.key]
	if !ok {
		return
	}
	fn(&st.ImportStatus)
	st.updated = time.Now()
}

// Running 记录累计计数（调用方按自己的节拍调，不必每条都调）。
func (h *importHandle) Running(read, indexed, failed, skipped, batches, bytes int64) {
	h.update(func(s *ImportStatus) {
		s.Read, s.Indexed, s.Failed, s.Skipped, s.Batches, s.Bytes =
			read, indexed, failed, skipped, batches, bytes
	})
}

// Note 附一句当前动作（压缩包场景用来显示正读到哪个文件）。
func (h *importHandle) Note(msg string) {
	h.update(func(s *ImportStatus) { s.Message = msg })
}

// Origin 记录当前来源文件。
func (h *importHandle) Origin(name string) {
	h.update(func(s *ImportStatus) { s.Origin = name })
}

// Pause 挂起以等用户决策：Waiting=true 并把跳过样例摆出来（前端据此弹决策框）。
func (h *importHandle) Pause(skipped int64, samples []string) {
	h.update(func(s *ImportStatus) {
		s.Waiting = true
		s.Skipped = skipped
		s.SkipSamples = samples
		s.Message = "跳过数量超过阈值，等待确认"
	})
}

// Resume 解除挂起（继续导入或中止时都要调，状态要对）。
func (h *importHandle) Resume() {
	h.update(func(s *ImportStatus) {
		s.Waiting = false
		s.Message = ""
	})
}

// Files 登记本次要处理的文件清单（抽屉展示用）；有清单时 FileStep 从 1 起。
func (h *importHandle) Files(list []string) {
	h.update(func(s *ImportStatus) {
		s.Files = list
		if len(list) > 0 {
			s.FileStep = 1
		}
	})
}

// FileStep 更新正在读取的文件序号（只在切换文件时调）。
func (h *importHandle) FileStep(n int) {
	h.update(func(s *ImportStatus) { s.FileStep = n })
}

// Finish 收尾：err 非空则落 error，并补上最终计数。
func (h *importHandle) Finish(err error, read, indexed, failed, skipped, batches, bytes int64) {
	h.update(func(s *ImportStatus) {
		s.Read, s.Indexed, s.Failed, s.Skipped, s.Batches, s.Bytes =
			read, indexed, failed, skipped, batches, bytes
		if err != nil {
			s.Phase = "error"
			s.Message = err.Error()
			return
		}
		s.Phase = "done"
	})
}

// ImportProgress GET /api/es/:id/import/progress?key=
//
// 进度是旁路信息：导入请求同步返回，这里只让前端在等待期间能看到进展。
// 查不到（已结束很久、条目被淘汰）如实回 found=false，前端据此停止轮询。
func (h *Handler) ImportProgress(c *gin.Context) {
	key := strings.TrimSpace(c.Query("key"))
	st, ok := h.imports.get(key)
	if !ok {
		resp.OK(c, gin.H{"found": false, "active": false})
		return
	}
	resp.OK(c, gin.H{"found": true, "active": st.Phase == "running", "status": st})
}

// importPauses 暂停决策注册表：runImport 跳过超阈值时登记一个 channel 并挂起，
// /import/resume 按 key 投递决策（continue / abort）把它唤醒。
type importPauses struct {
	mu sync.Mutex
	m  map[string]chan string
}

func newImportPauses() *importPauses { return &importPauses{m: map[string]chan string{}} }

func (p *importPauses) register(key string) chan string {
	ch := make(chan string, 1)
	if p == nil || key == "" {
		return ch // 没有 key（测试/直调）：给个孤儿 channel，永远等不到决策
	}
	p.mu.Lock()
	p.m[key] = ch
	p.mu.Unlock()
	return ch
}

func (p *importPauses) unregister(key string) {
	if p == nil || key == "" {
		return
	}
	p.mu.Lock()
	delete(p.m, key)
	p.mu.Unlock()
}

// tell 投递决策；没有登记中的暂停返回 false。
func (p *importPauses) tell(key, action string) bool {
	if p == nil || key == "" {
		return false
	}
	p.mu.Lock()
	ch, ok := p.m[key]
	p.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- action:
		return true
	default: // channel 已满（重复判断）：视为没接住
		return false
	}
}

// ImportResume POST /api/es/:id/import/resume { key, action }
// 暂停决策出口：continue = 继续导入（此后不再二次暂停）；abort = 中止导入，
// 前端接到错误后转入「重新生成 pattern → 删除索引重建重写」的补救链。
func (h *Handler) ImportResume(c *gin.Context) {
	var req struct {
		Key    string `json:"key"`
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.Action != "continue" && req.Action != "abort" {
		resp.Fail(c, resp.CodeBadRequest, "action 只能是 continue 或 abort")
		return
	}
	if !h.pauses.tell(strings.TrimSpace(req.Key), req.Action) {
		resp.Fail(c, esCodeImportInvalid, "没有处于暂停等待的导入任务")
		return
	}
	resp.OK(c, gin.H{"ok": true})
}
