// 任务编排：定义 CRUD、按步骤栅栏(by_step)执行引擎与运行进度 SSE。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store"
)

// orchHandler 任务编排。
type orchHandler struct {
	repo     *store.OrchestrationRepo
	tplRepo  *store.DeployRepo
	hostRepo *store.HostRepo
	credRepo *store.CredentialRepo
	cryptoS  *icrypto.Service
	sshC     *sshx.Client
	bus      *eventbus.Bus
	auditRepo *store.AuditRepo
}

func NewOrchHandler(repo *store.OrchestrationRepo, tplRepo *store.DeployRepo, hostRepo *store.HostRepo,
	credRepo *store.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *store.AuditRepo) *orchHandler {
	return &orchHandler{repo: repo, tplRepo: tplRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo}
}

// ---------- 定义 CRUD ----------

type orchSaveReq struct {
	Name        string                    `json:"name" binding:"required"`
	Description string                    `json:"description"`
	ExecMode    string                    `json:"exec_mode"`
	Enabled     *bool                     `json:"enabled"`
	Steps       []model.OrchestrationStep `json:"steps" binding:"required,min=1,dive"`
}

func (h *orchHandler) List(c *gin.Context) {
	items, err := h.repo.List()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询编排失败")
		return
	}
	if items == nil {
		items = []model.Orchestration{}
	}
	resp.OK(c, items)
}

func (h *orchHandler) Get(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	o, steps, err := h.repo.Get(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询编排失败")
		return
	}
	if o == nil {
		resp.Fail(c, resp.CodeNotFound, "编排不存在")
		return
	}
	resp.OK(c, gin.H{"orchestration": o, "steps": steps})
}

func (h *orchHandler) Save(c *gin.Context) {
	var req orchSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	mode := req.ExecMode
	if mode == "" {
		mode = "by_step"
	}
	if mode != "by_step" && mode != "by_host" {
		resp.Fail(c, resp.CodeBadRequest, "exec_mode 仅支持 by_step / by_host")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	o := &model.Orchestration{Name: req.Name, Description: req.Description, ExecMode: mode, Enabled: enabled}
	if id, _ := strconv.ParseInt(c.Param("id"), 10, 64); id > 0 {
		o.ID = id
	}
	// 步骤参数默认补全
	for i := range req.Steps {
		if req.Steps[i].ParamsJSON == "" {
			req.Steps[i].ParamsJSON = "{}"
		}
		if req.Steps[i].RetryIntervalSec <= 0 {
			req.Steps[i].RetryIntervalSec = 30
		}
	}
	savedID, err := h.repo.Save(o, req.Steps)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			resp.Fail(c, resp.CodeConflict, "编排名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "保存编排失败")
		return
	}
	action := "orchestration.create"
	if o.ID > 0 {
		action = "orchestration.update"
	}
	h.auditRepo.Create(&model.AuditLog{Action: action, TargetType: "orchestration",
		TargetID: savedID, Detail: fmt.Sprintf("name=%s steps=%d", o.Name, len(req.Steps)), RemoteIP: c.ClientIP()})
	resp.OK(c, gin.H{"id": savedID})
}

func (h *orchHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除编排失败")
		return
	}
	h.auditRepo.Create(&model.AuditLog{Action: "orchestration.delete", TargetType: "orchestration", TargetID: id, RemoteIP: c.ClientIP()})
	resp.OK(c, nil)
}

// ---------- 运行 ----------

type orchRunReq struct {
	HostIDs []int64 `json:"host_ids" binding:"required,min=1"`
}

// Run POST /api/orchestrations/:id/run：创建运行并异步执行（P1：by_step 栅栏语义）。
func (h *orchHandler) Run(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	o, stepsDef, err := h.repo.Get(id)
	if err != nil || o == nil {
		resp.Fail(c, resp.CodeNotFound, "编排不存在")
		return
	}
	if !o.Enabled {
		resp.Fail(c, resp.CodeBadRequest, "编排已停用")
		return
	}
	var req orchRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	hostIDs := dedupInt64(req.HostIDs)

	var hosts []model.DeployTaskHost
	for _, hid := range hostIDs {
		hh, err := h.hostRepo.GetByID(hid)
		if err != nil || hh == nil {
			continue
		}
		hosts = append(hosts, model.DeployTaskHost{HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP})
	}
	if len(hosts) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "没有有效主机")
		return
	}

	runSteps := make([]model.OrchestrationRunStep, 0, len(hosts)*len(stepsDef))
	for _, def := range stepsDef { // seq 升序（repo 已排序）
		for _, hh := range hosts {
			runSteps = append(runSteps, model.OrchestrationRunStep{
				HostID: hh.HostID, HostName: hh.HostName, HostIP: hh.HostIP,
				Seq: def.Seq, TemplateID: def.TemplateID, TemplateName: def.TemplateName,
			})
		}
	}
	hostIDsJSON, _ := json.Marshal(hostIDs)
	run := &model.OrchestrationRun{OrchestrationID: id, Name: o.Name, ExecMode: o.ExecMode,
		TotalHosts: len(hosts), TriggerType: "manual", HostIDs: string(hostIDsJSON)}
	runID, err := h.repo.CreateRun(run, runSteps)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建运行失败")
		return
	}
	h.auditRepo.Create(&model.AuditLog{Action: "orchestration.run", TargetType: "orchestration_run",
		TargetID: runID, Detail: fmt.Sprintf("name=%s hosts=%d steps=%d", o.Name, len(hosts), len(stepsDef)), RemoteIP: c.ClientIP()})

	go h.executeRun(runID)
	resp.OK(c, gin.H{"run_id": runID})
}

// orchProgress SSE 推送的运行事件。
type orchProgress struct {
	RunID     int64  `json:"run_id"`
	HostID    int64  `json:"host_id"`
	Seq       int    `json:"seq"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	RunStatus string `json:"run_status"` // running 或终态
	OkHosts   int    `json:"ok_hosts,omitempty"`
	FailHosts int    `json:"fail_hosts,omitempty"`
	Total     int    `json:"total,omitempty"`
}

// executeRun 执行引擎：逐步骤推进，每步在全部目标主机完成后才进入下一步（栅栏）；
// 主机之间并发，同一主机天然串行。失败且未开 continue_on_error 时该主机后续步骤置 skipped。
func (h *orchHandler) executeRun(runID int64) {
	run, err := h.repo.GetRun(runID)
	if err == nil && run == nil {
		return
	}
	if run == nil {
		return
	}
	_, stepsDef, err := h.repo.Get(run.OrchestrationID)
	if err != nil || len(stepsDef) == 0 {
		_, _ = h.repo.FinishRun(runID)
		h.publishDone(runID, "failed", 0, 0, 0)
		return
	}
	allRows, err := h.repo.RunSteps(runID)
	if err != nil || len(allRows) == 0 {
		_, _ = h.repo.FinishRun(runID)
		return
	}

	publishStep := func(rec store.RunStepRef, status string, output, errMsg string, attempt int, runStatus string) {
		if h.bus == nil {
			return
		}
		h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
			HostID: rec.HostID, Seq: rec.Seq, Status: status, Output: output, Error: errMsg,
			Attempt: attempt, RunStatus: runStatus})
	}

	concurrency := sysutil.AdaptiveConcurrency(run.TotalHosts)
	sem := make(chan struct{}, concurrency)

	// 按步骤序号推进（栅栏）
	for _, def := range stepsDef {
		var tpl *model.DeployTemplate
		if t, terr := h.tplRepo.GetTemplate(def.TemplateID); terr == nil {
			tpl = t
		}
		var batch []store.RunStepRef
		for _, r := range allRows {
			if r.Seq == def.Seq {
				batch = append(batch, r)
			}
		}

		var wg sync.WaitGroup
		for i := range batch {
			cell := batch[i]
			// 上一步失败已跳过的主机不再执行
			if cell.Status == "skipped" {
				continue
			}
			wg.Add(1)
			go func(cell store.RunStepRef) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				rec := cell // 可变副本（回填输出）
				attempts := def.RetryCount + 1
				if attempts < 1 {
					attempts = 1
				}
				var lastOut, lastErr string

				for a := 1; a <= attempts; a++ {
					_ = h.repo.UpdateRunStepStatus(rec.RecID, "running", a, "", "")
					publishStep(cell, "running", "", "", a, "running")

					var rendered string
					var rerr error
					if tpl == nil {
						rerr = fmt.Errorf("模板不存在或已删除")
					} else {
						var params map[string]string
						_ = json.Unmarshal([]byte(def.ParamsJSON), &params)
						rendered, rerr = renderScript(tpl.Script, tpl.Variables, params)
						if rerr == nil {
							hr := store.HostRecord{DeployTaskHost: model.DeployTaskHost{HostID: cell.HostID, HostName: cell.HostName, HostIP: cell.HostIP}}
							rendered = applyHostVars(rendered, cell.Seq, hr)
						}
					}
					if rerr != nil {
						lastErr = rerr.Error()
						break // 渲染错误重试无意义
					}

					onLog := func(chunk string) {
						if chunk == "" || h.bus == nil {
							return
						}
						h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
							HostID: cell.HostID, Seq: cell.Seq, Status: "output", Output: chunk, Attempt: a, RunStatus: "running"})
					}
					out, execErr := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, cell.HostID, rendered, onLog)
					lastOut, lastErr = out, ""
					if execErr != nil {
						lastErr = execErr.Error()
						if a < attempts {
							time.Sleep(time.Duration(def.RetryIntervalSec) * time.Second)
						}
						continue
					}
					lastErr = ""
					break
				}

				status := "success"
				if lastErr != "" {
					status = "failed"
				}
				_ = h.repo.UpdateRunStepStatus(rec.RecID, status, attempts, lastOut, lastErr)
				if status == "success" && tpl != nil {
					if err := h.tplRepo.MarkHostInstalled(cell.HostID, def.TemplateID, def.TemplateName, runID); err != nil {
						fmt.Printf("orchestration: 标记安装失败 host=%d: %v\n", cell.HostID, err)
					}
				}
				publishStep(cell, status, lastOut, lastErr, attempts, "running")

				if status == "failed" && !def.ContinueOnError {
					_ = h.repo.SkipRemaining(runID, cell.HostID, cell.Seq)
					if h.bus != nil { // 通知前端该主机剩余步骤被跳过
						h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
							HostID: cell.HostID, Status: "skipped-refresh", RunStatus: "running"})
					}
				}
			}(cell)
		}
		wg.Wait() // 栅栏：本步全员终态后才进入下一步
	}

	finalStatus, _ := h.repo.FinishRun(runID)
	if r2, e2 := h.repo.GetRun(runID); e2 == nil && r2 != nil {
		if h.bus != nil {
			h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
				Status: "finished", RunStatus: finalStatus, OkHosts: r2.OkHosts, FailHosts: r2.FailHosts, Total: r2.TotalHosts})
		}
	} else {
		h.publishDone(runID, finalStatus, 0, 0, run.TotalHosts)
	}
}

func (h *orchHandler) publishDone(runID int64, status string, okN, failN, total int) {
	if h.bus == nil {
		return
	}
	h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
		Status: "finished", RunStatus: status, OkHosts: okN, FailHosts: failN, Total: total})
}

// ---------- 运行历史 & 明细 ----------

func (h *orchHandler) RunsList(c *gin.Context) {
	page, pageSize := parsePage(c)
	items, total, err := h.repo.ListRuns(page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询运行历史失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *orchHandler) RunsDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	run, err := h.repo.GetRun(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询运行失败")
		return
	}
	if run == nil {
		resp.Fail(c, resp.CodeNotFound, "运行不存在")
		return
	}
	rows, err := h.repo.RunSteps(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询运行明细失败")
		return
	}
	steps := make([]model.OrchestrationRunStep, 0, len(rows))
	for _, r := range rows {
		steps = append(steps, r.OrchestrationRunStep)
	}
	resp.OK(c, gin.H{"run": run, "steps": steps})
}

// SSEOrchestration GET /api/sse/orchestration?run_id=N：单次运行的实时进度流。
func (h *orchHandler) SSEOrchestration(c *gin.Context) {
	runID, err := strconv.ParseInt(c.Query("run_id"), 10, 64)
	if err != nil || runID <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "run_id 无效")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, "streaming not supported")
		return
	}

	ch := make(chan orchProgress, 128)
	var subID int
	if h.bus != nil {
		subID = h.bus.Subscribe(eventbus.TopicOrchestrationProgress, func(ev eventbus.Event) {
			if p, ok := ev.Data.(orchProgress); ok && p.RunID == runID {
				select {
				case ch <- p:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicOrchestrationProgress, subID)
	}

	c.SSEvent("connected", runID)
	flusher.Flush()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case p := <-ch:
			data, _ := json.Marshal(p)
			if p.Status == "finished" {
				c.SSEvent("done", string(data))
				flusher.Flush()
				return false
			}
			c.SSEvent("progress", string(data))
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		case <-c.Request.Context().Done():
			return false
		}
		flusher.Flush()
		return true
	})
}
