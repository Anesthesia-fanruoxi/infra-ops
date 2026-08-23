// 定时任务 API 与 cron 调度器：到点触发部署执行引擎。
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

// deployScheduleHandler 定时任务 CRUD 与启停。
type deployScheduleHandler struct {
	schedRepo *store.DeployScheduleRepo
	tplRepo   *store.DeployRepo
	dh        *deployHandler
}

func NewDeployScheduleHandler(schedRepo *store.DeployScheduleRepo, tplRepo *store.DeployRepo, dh *deployHandler) *deployScheduleHandler {
	return &deployScheduleHandler{schedRepo: schedRepo, tplRepo: tplRepo, dh: dh}
}

type scheduleReq struct {
	Name       string            `json:"name" binding:"required"`
	TemplateID int64             `json:"template_id" binding:"required"`
	HostIDs    []int64           `json:"host_ids"` // 可为空：仅保存配置不执行
	Params     map[string]string `json:"params"`
	CronExpr   string            `json:"cron_expr" binding:"required"`
}

// List GET /api/deploy/schedules
func (h *deployScheduleHandler) List(c *gin.Context) {
	items, err := h.schedRepo.ListSchedules()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询定时任务失败")
		return
	}
	if items == nil {
		items = []model.DeploySchedule{}
	}
	resp.OK(c, items)
}

// Create POST /api/deploy/schedules
func (h *deployScheduleHandler) Create(c *gin.Context) {
	req, ok := h.bindAndValidate(c)
	if !ok {
		return
	}
	s := &model.DeploySchedule{
		Name: req.Name, TemplateID: req.TemplateID,
		HostIDs: mustMarshal(req.HostIDs), Params: mustMarshal(req.Params),
		CronExpr: req.CronExpr, Enabled: true,
	}
	id, err := h.schedRepo.CreateSchedule(s)
	if err != nil {
		resp.Fail(c, resp.CodeConflict, "创建失败：名称可能已存在")
		return
	}
	if h.dh.sched != nil {
		h.dh.sched.reload(id)
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/deploy/schedules/:id
func (h *deployScheduleHandler) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	existing, err := h.schedRepo.GetSchedule(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "定时任务不存在")
		return
	}
	req, ok := h.bindAndValidate(c)
	if !ok {
		return
	}
	existing.Name = req.Name
	existing.TemplateID = req.TemplateID
	existing.HostIDs = mustMarshal(req.HostIDs)
	existing.Params = mustMarshal(req.Params)
	existing.CronExpr = req.CronExpr
	if err := h.schedRepo.UpdateSchedule(existing); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新失败")
		return
	}
	if h.dh.sched != nil {
		h.dh.sched.reload(id)
	}
	resp.OK(c, nil)
}

// Delete DELETE /api/deploy/schedules/:id
func (h *deployScheduleHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	existing, err := h.schedRepo.GetSchedule(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "定时任务不存在")
		return
	}
	if h.dh.sched != nil {
		h.dh.sched.removeEntry(id)
	}
	if err := h.schedRepo.DeleteSchedule(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除失败")
		return
	}
	resp.OK(c, nil)
}

// Toggle POST /api/deploy/schedules/:id/toggle：启动/停止。
func (h *deployScheduleHandler) Toggle(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Enabled *bool `json:"enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	if err := h.schedRepo.SetEnabled(id, *req.Enabled); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新失败")
		return
	}
	if h.dh.sched != nil {
		h.dh.sched.reload(id)
	}
	resp.OK(c, nil)
}

// Runs GET /api/deploy/schedules/:id/runs：历次触发记录。
func (h *deployScheduleHandler) Runs(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	page, pageSize := parsePage(c)
	items, total, err := h.schedRepo.ListTasksBySchedule(id, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询触发历史失败")
		return
	}
	if items == nil {
		items = []model.DeployTask{}
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

// bindAndValidate 绑定请求并校验 cron 表达式合法性。
func (h *deployScheduleHandler) bindAndValidate(c *gin.Context) (*scheduleReq, bool) {
	var req scheduleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return nil, false
	}
	if _, err := cron.ParseStandard(req.CronExpr); err != nil {
		resp.Fail(c, resp.CodeBadRequest, fmt.Sprintf("cron 表达式非法: %v", err))
		return nil, false
	}
	return &req, true
}

// deployScheduler cron 调度器：维护 schedule 与 cron entry 的映射。
type deployScheduler struct {
	h   *deployHandler
	c   *cron.Cron
	mu  sync.Mutex
	ids map[int64]cron.EntryID
}

func newScheduler(h *deployHandler) *deployScheduler {
	return &deployScheduler{h: h, c: cron.New(), ids: map[int64]cron.EntryID{}}
}

func (s *deployScheduler) start() {
	s.c.Start()
	s.loadAll()
}

// loadAll 服务启动时加载全部启用项。
func (s *deployScheduler) loadAll() {
	list, err := s.h.schedRepo.ListSchedules()
	if err != nil {
		log.Printf("[scheduler] 加载定时任务失败: %v", err)
		return
	}
	n := 0
	for i := range list {
		if list[i].Enabled {
			s.addEntry(&list[i])
			n++
		}
	}
	log.Printf("[scheduler] 已加载 %d 个定时任务", n)
}

func (s *deployScheduler) addEntry(sc *model.DeploySchedule) {
	entryID, err := s.c.AddFunc(sc.CronExpr, func() { s.fire(sc.ID) })
	if err != nil {
		log.Printf("[scheduler] 注册任务 %d(%s) 失败: %v", sc.ID, sc.CronExpr, err)
		return
	}
	s.mu.Lock()
	s.ids[sc.ID] = entryID
	s.mu.Unlock()
	s.refreshNextRun(sc.ID, sc.CronExpr)
}

func (s *deployScheduler) removeEntry(scheduleID int64) {
	s.mu.Lock()
	id, ok := s.ids[scheduleID]
	delete(s.ids, scheduleID)
	s.mu.Unlock()
	if ok {
		s.c.Remove(id)
	}
}

// reload 增删改/启停后刷新对应 entry。
func (s *deployScheduler) reload(scheduleID int64) {
	s.removeEntry(scheduleID)
	sc, err := s.h.schedRepo.GetSchedule(scheduleID)
	if err != nil || sc == nil || !sc.Enabled {
		return
	}
	s.addEntry(sc)
}

// fire 到点触发：防堆积 → 实时读模板最新内容 → 复用执行链路。
func (s *deployScheduler) fire(scheduleID int64) {
	busy, err := s.h.schedRepo.HasRunningTaskForSchedule(scheduleID)
	if err == nil && busy {
		log.Printf("[scheduler] 任务 %d 上次执行未完成，跳过本次触发", scheduleID)
		return
	}
	sc, err := s.h.schedRepo.GetSchedule(scheduleID)
	if err != nil || sc == nil || !sc.Enabled {
		return
	}
	tpl, err := s.h.tplRepo.GetTemplate(sc.TemplateID)
	if err != nil || tpl == nil {
		log.Printf("[scheduler] 任务 %d 关联模板不存在，跳过", scheduleID)
		return
	}

	var hostIDs []int64
	_ = json.Unmarshal(sc.HostIDs, &hostIDs)
	var params map[string]string
	_ = json.Unmarshal(sc.Params, &params)

	taskID, err := s.h.createAndRun(tpl, hostIDs, params, "schedule", scheduleID, "scheduler")
	if err != nil {
		log.Printf("[scheduler] 任务 %d 触发失败: %v", scheduleID, err)
		return
	}
	_ = s.h.schedRepo.UpdateRunInfo(scheduleID, taskID)
	s.refreshNextRun(scheduleID, sc.CronExpr)
}

// refreshNextRun 计算并回写下次触发时间（展示用）。
func (s *deployScheduler) refreshNextRun(scheduleID int64, expr string) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return
	}
	next := sched.Next(time.Now()).Format("2006-01-02 15:04:05")
	_ = s.h.schedRepo.SetNextRun(scheduleID, next)
}
