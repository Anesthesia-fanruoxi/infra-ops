// ai_catalog_task.go 语义目录生成的「后台任务」：一次可跨多个库，逐库串行跑。
//
// 与导出 / 导入的进度注册表（progress.go）同一思路，但更进一步：那边的请求本身
// 仍是同步的，进度只是旁路；这里的生成任务脱离 HTTP 请求生命周期跑在后台——
// 前端点完「开始生成」即可关掉弹框 / 切走页面，任务照跑，随时回来查进度或停止。
// 所以 ctx 不再派生自 c.Request.Context()（客户端一断开就会被取消），而是
// 独立的 background + 超时，只有显式「停止」或整体超时才结束。
//
// 每个连接只保留「最近一个」任务：终结后留在注册表里供前端恢复「失败重试 / 已停止」
// 状态，直到下一次启动新任务时被替换——天然无内存增长，也不存在任务泄漏。
package mysql

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// CatalogTaskSchema 任务里单个库的处理状态（下发给前端）。
type CatalogTaskSchema struct {
	Schema    string `json:"schema"`
	State     string `json:"state"` // pending / running / done / stopped / failed
	Total     int    `json:"total"`
	Generated int    `json:"generated"`
	Cached    int    `json:"cached"`
	Failed    int    `json:"failed"`
	Message   string `json:"message,omitempty"`
}

// CatalogTaskRecord 任务里已完成的一批调用记录（下发给前端弹框的「生成记录」列表）。
// 与「调用记录」表同一口径：一次模型调用一行，成败、token、耗时都记下来，
// 前端按任务进度即收即显，不用另查记录页。
type CatalogTaskRecord struct {
	Time             string `json:"time"` // HH:MM:SS
	Schema           string `json:"schema"`
	BatchNo          int    `json:"batch_no"`
	BatchTotal       int    `json:"batch_total"`
	Tables           int    `json:"tables"`
	Status           string `json:"status"` // ok / error
	Message          string `json:"message,omitempty"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	Estimated        bool   `json:"estimated,omitempty"` // 接口没回 usage，按字符估算
	ElapsedMs        int64  `json:"elapsed_ms"`
}

// catalogBatchProgress 批级进度事件：批计数 + 这一批的生成记录。
// buildSchemaCatalog 每批结束（成功 / 失败都算）回调一次。
type catalogBatchProgress struct {
	Done   int
	Total  int
	Record CatalogTaskRecord
}

// CatalogTaskStatus 目录生成任务的完整快照（前端轮询查看进度用）。
type CatalogTaskStatus struct {
	TaskID     string              `json:"task_id"`
	State      string              `json:"state"` // running / done / stopped
	Total      int                 `json:"total"` // 库总数
	Finished   int                 `json:"finished"`
	Current    string              `json:"current"`     // 正在处理的库
	BatchDone  int                 `json:"batch_done"`  // 当前库的批进度
	BatchTotal int                 `json:"batch_total"` // 当前库的总批数（0 = 还在读表结构）
	Schemas    []CatalogTaskSchema `json:"schemas"`
	Records    []CatalogTaskRecord `json:"records,omitempty"` // 每批一次的生成记录（按时间顺序追加）
	Message    string              `json:"message"`
	Usage      model.MySQLAIUsage  `json:"usage"`
	ElapsedMs  int64               `json:"elapsed_ms"`
}

// catalogTask 单个后台任务的运行时载体；字段都在 registry 的锁下读写。
type catalogTask struct {
	status  CatalogTaskStatus
	force   bool
	cancel  context.CancelFunc
	started time.Time
}

// catalogTaskRegistry 每连接一个「最近任务」的注册表。
type catalogTaskRegistry struct {
	mu sync.Mutex
	m  map[int64]*catalogTask
}

func newCatalogTaskRegistry() *catalogTaskRegistry {
	return &catalogTaskRegistry{m: map[int64]*catalogTask{}}
}

// get 取某连接的最近任务快照；没有任务返回 false。
func (r *catalogTaskRegistry) get(connID int64) (CatalogTaskStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.m[connID]
	if !ok {
		return CatalogTaskStatus{}, false
	}
	return t.snapshotLocked(), true
}

// setFields 在锁内改动任务状态；任务侧所有写入口都走它，避免散落的锁逻辑。
func (r *catalogTaskRegistry) setFields(t *catalogTask, fn func(s *CatalogTaskStatus)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&t.status)
}

// snapshotLocked 复制一份快照（含 schemas / records 数组），避免调用方拿到内部引用；调用方持有 r.mu。
func (t *catalogTask) snapshotLocked() CatalogTaskStatus {
	out := t.status
	out.Schemas = append([]CatalogTaskSchema(nil), t.status.Schemas...)
	out.Records = append([]CatalogTaskRecord(nil), t.status.Records...)
	out.ElapsedMs = time.Since(t.started).Milliseconds()
	return out
}

// catalogBatchRecord 把一批调用的结果装配成生成记录行；
// 用量口径与「调用记录」表一致（接口没回 usage 就标 estimated），失败带上截断后的原因。
func catalogBatchRecord(schema string, no, total, tables int, started time.Time, res *aiopenai.Result, err error) CatalogTaskRecord {
	rec := CatalogTaskRecord{
		Time:       time.Now().Format("15:04:05"),
		Schema:     schema,
		BatchNo:    no,
		BatchTotal: total,
		Tables:     tables,
		Status:     model.LogStatusOK,
		ElapsedMs:  time.Since(started).Milliseconds(),
	}
	if res != nil {
		rec.PromptTokens = int64(res.Usage.PromptTokens)
		rec.CompletionTokens = int64(res.Usage.CompletionTokens)
		rec.TotalTokens = int64(res.Usage.TotalTokens)
		rec.Estimated = !res.Usage.FromAPI
	}
	if err != nil {
		rec.Status = model.LogStatusError
		rec.Message = truncateRunes(err.Error(), logErrMax)
	}
	return rec
}

// StartCatalogTask POST /api/mysql/:id/ai/catalog/task
//
// 启动后台批量生成；已有运行中的任务时不再另起（重复提交没有意义、只会双倍花 token），
// 直接返回现有任务的快照，由前端切到进度视图。响应里 started 区分这两者。
func (h *Handler) StartCatalogTask(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req catalogReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	client, aiCfg, err := h.aiClient()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}

	// 快速路径：已有运行中的任务直接回快照，不做库列表解析
	h.catalogTasks.mu.Lock()
	if t, exists := h.catalogTasks.m[conn.ID]; exists && t.status.State == "running" {
		snap := t.snapshotLocked()
		h.catalogTasks.mu.Unlock()
		resp.OK(c, gin.H{"task": snap, "started": false})
		return
	}
	h.catalogTasks.mu.Unlock()

	// 任务 ctx 独立于请求：客户端断开不会牵连任务；cancel 交给「停止」接口异步触发
	ctx, cancel := context.WithTimeout(context.Background(), catalogTotalTimeout)
	// 库列表解析要在启动前完成：参数错误、连不上这类问题当场回给前端，
	// 不要进了后台任务才失败（那样用户只能看到一条 running 转 stopped 的莫名其妙状态）。
	// 解析可能建连（秒级），必须放在注册表锁外：持锁会卡住所有连接的任务轮询与停止操作
	schemas, code, err := h.resolveCatalogSchemas(ctx, conn, &req)
	if err != nil {
		cancel()
		resp.Fail(c, code, err.Error())
		return
	}

	h.catalogTasks.mu.Lock()
	// 解析期间可能已有任务抢先启动：让先到的跑，本次只回快照
	if t, exists := h.catalogTasks.m[conn.ID]; exists && t.status.State == "running" {
		snap := t.snapshotLocked()
		h.catalogTasks.mu.Unlock()
		cancel()
		resp.OK(c, gin.H{"task": snap, "started": false})
		return
	}

	task := &catalogTask{
		status: CatalogTaskStatus{
			TaskID:  fmt.Sprintf("cat-%d-%d", conn.ID, time.Now().UnixNano()),
			State:   "running",
			Total:   len(schemas),
			Schemas: make([]CatalogTaskSchema, 0, len(schemas)),
		},
		force:   req.Force,
		cancel:  cancel,
		started: time.Now(),
	}
	for _, s := range schemas {
		task.status.Schemas = append(task.status.Schemas, CatalogTaskSchema{Schema: s, State: "pending"})
	}
	h.catalogTasks.m[conn.ID] = task
	snap := task.snapshotLocked()
	h.catalogTasks.mu.Unlock()

	go func() {
		defer cancel()
		h.runCatalogTask(ctx, conn, client, aiCfg, task)
	}()
	resp.OK(c, gin.H{"task": snap, "started": true})
}

// GetCatalogTask GET /api/mysql/:id/ai/catalog/task
//
// 任务快照；从未启动过则 task 为 null。终结后的任务保留在注册表里，
// 前端刷新页面 / 切回页签时据此恢复「失败重试 / 已停止」的界面状态。
func (h *Handler) GetCatalogTask(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	snap, found := h.catalogTasks.get(conn.ID)
	if !found {
		resp.OK(c, gin.H{"task": nil})
		return
	}
	resp.OK(c, gin.H{"task": snap})
}

// StopCatalogTask POST /api/mysql/:id/ai/catalog/task/stop
//
// 停止运行中的任务：取消 ctx 后当前模型调用即刻中止，循环在下一个检查点退出，
// 已跑完的批次早已逐批落库、不受影响。取消是异步收尾的，这里返回的快照
// 可能仍是 running（收尾中），前端继续轮询到 stopped 即可；重复调用幂等。
func (h *Handler) StopCatalogTask(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	h.catalogTasks.mu.Lock()
	t, exists := h.catalogTasks.m[conn.ID]
	if !exists {
		h.catalogTasks.mu.Unlock()
		resp.OK(c, gin.H{"task": nil})
		return
	}
	if t.status.State == "running" && t.cancel != nil {
		t.cancel()
	}
	snap := t.snapshotLocked()
	h.catalogTasks.mu.Unlock()
	resp.OK(c, gin.H{"task": snap})
}

// runCatalogTask 后台任务主体：逐库串行调用 buildSchemaCatalog。
// 每库独立读表结构、独立落库；单库失败不拖垮其余库（与同步版口径一致）。
func (h *Handler) runCatalogTask(ctx context.Context, conn *model.MySQLConn, client *aiopenai.Client,
	aiCfg aiSettings, task *catalogTask) {

	totalUsage := model.MySQLAIUsage{}
	for i := 0; i < len(task.status.Schemas); i++ {
		var schema string
		h.catalogTasks.setFields(task, func(s *CatalogTaskStatus) {
			if ctx.Err() != nil { // 停止 / 超时：该库还没开始，保持 pending
				return
			}
			schema = s.Schemas[i].Schema
			s.Current = schema
			s.BatchDone, s.BatchTotal = 0, 0
			s.Schemas[i].State = "running"
		})
		if schema == "" { // 上面的检查点发现 ctx 已取消
			h.finishCatalogTask(task, i, "stopped", totalUsage)
			return
		}

		st, err := h.buildSchemaCatalog(ctx, conn, client, aiCfg, schema, task.force,
			func(ev catalogBatchProgress) { // 批级进度回调：批计数实时可信 + 生成记录逐条追加
				h.catalogTasks.setFields(task, func(s *CatalogTaskStatus) {
					s.BatchDone, s.BatchTotal = ev.Done, ev.Total
					s.Records = append(s.Records, ev.Record)
				})
			})
		if err != nil {
			st = &model.MySQLAICatalogState{Schema: schema, Message: err.Error()}
		}
		addUsage(&totalUsage, st.Usage)

		// ctx 已取消说明这一库是被停下来的（可能只跑了一半，已完成的批次已落库）
		if ctx.Err() != nil {
			h.catalogTasks.setFields(task, func(s *CatalogTaskStatus) {
				item := &s.Schemas[i]
				item.State = "stopped"
				item.Total = st.Total
				item.Generated = st.Generated
				item.Cached = st.Cached
				item.Failed = st.Failed
				item.Message = st.Message
			})
			h.finishCatalogTask(task, i+1, "stopped", totalUsage)
			return
		}

		h.catalogTasks.setFields(task, func(s *CatalogTaskStatus) {
			item := &s.Schemas[i]
			item.Total = st.Total
			item.Generated = st.Generated
			item.Cached = st.Cached
			item.Failed = st.Failed
			item.Message = st.Message
			if st.Total == 0 && st.Generated == 0 && st.Cached == 0 {
				item.State = "failed" // 整库级失败：连表列表都没读到
			} else {
				item.State = "done"
			}
			s.Finished = i + 1
		})
	}
	h.finishCatalogTask(task, len(task.status.Schemas), "done", totalUsage)
}

// finishCatalogTask 收尾：状态、汇总 message 与用量一次性落定。
// finished 传已完成库数（停止时 = 中断位置），state 传 done / stopped。
func (h *Handler) finishCatalogTask(task *catalogTask, finished int, state string, totalUsage model.MySQLAIUsage) {
	h.catalogTasks.setFields(task, func(s *CatalogTaskStatus) {
		s.Finished = finished
		s.State = state
		s.Current = ""
		s.BatchDone, s.BatchTotal = 0, 0
		s.Usage = totalUsage

		generated, cached, failed := 0, 0, 0
		for _, item := range s.Schemas {
			generated += item.Generated
			cached += item.Cached
			failed += item.Failed
		}
		if state == "stopped" {
			s.Message = fmt.Sprintf("已停止：完成 %d/%d 个库（新生成 %d 张表，命中缓存 %d，失败 %d）；已完成的部分已保留，未完成的库可再次生成补齐",
				finished, s.Total, generated, cached, failed)
			return
		}
		s.Message = fmt.Sprintf("共 %d 个库：新生成 %d 张表，命中缓存 %d，失败 %d", s.Total, generated, cached, failed)
		if failed > 0 {
			s.Message += "；存在失败，详情见各库状态"
		}
	})
}
