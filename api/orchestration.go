// 任务编排：编排 = 多个「部署单元」（模板+该步骤主机集+逐台变量）按顺序串行的长任务。
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
	repo      *store.OrchestrationRepo
	tplRepo   *store.DeployRepo
	hostRepo  *store.HostRepo
	credRepo  *store.CredentialRepo
	cryptoS   *icrypto.Service
	sshC      *sshx.Client
	bus       *eventbus.Bus
	auditRepo *store.AuditRepo
}

func NewOrchHandler(repo *store.OrchestrationRepo, tplRepo *store.DeployRepo, hostRepo *store.HostRepo,
	credRepo *store.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *store.AuditRepo) *orchHandler {
	return &orchHandler{repo: repo, tplRepo: tplRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo}
}

// ---------- 定义 CRUD ----------

type orchStepReq struct {
	TemplateID       int64                        `json:"template_id" binding:"required"`
	Params           map[string]string            `json:"params"`   // 步骤级默认参数
	HostIDs          []int64                      `json:"host_ids" binding:"required,min=1"` // 本步骤目标主机
	HostVars         map[string]map[string]string `json:"host_vars"` // 主机覆盖: hostID -> {k:v}
	ContinueOnError  bool                         `json:"continue_on_error"`
	RetryCount       int                          `json:"retry_count"`
	RetryIntervalSec int                          `json:"retry_interval_sec"`
}

type orchSaveReq struct {
	Name        string         `json:"name" binding:"required"`
	Description string         `json:"description"`
	Enabled     *bool          `json:"enabled"`
	Steps       []orchStepReq  `json:"steps" binding:"required,min=1,dive"`
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

// stepResp / varResp 构造对前端友好的响应结构（params/host_vars 为对象而非 JSON 字符串）。
func (h *orchHandler) buildStepsResponse(steps []model.OrchestrationStep, svars []model.OrchestrationStepVar) []gin.H {
	varsBySeqHost := map[int]map[int64]map[string]string{}
	for _, sv := range svars {
		m := map[string]string{}
		_ = json.Unmarshal([]byte(sv.ParamsJSON), &m)
		if varsBySeqHost[sv.Seq] == nil {
			varsBySeqHost[sv.Seq] = map[int64]map[string]string{}
		}
		varsBySeqHost[sv.Seq][sv.HostID] = m
	}
	out := make([]gin.H, 0, len(steps))
	for _, st := range steps {
		var hostIDs []int64
		_ = json.Unmarshal([]byte(st.HostScope), &hostIDs)
		pm := map[string]string{}
		_ = json.Unmarshal([]byte(st.ParamsJSON), &pm)
		hv := map[string]map[string]string{}
		for hid, m := range varsBySeqHost[st.Seq] {
			hv[strconv.FormatInt(hid, 10)] = m
		}
		out = append(out, gin.H{
			"seq": st.Seq, "template_id": st.TemplateID, "template_name": st.TemplateName,
			"params": pm, "host_ids": hostIDs, "host_vars": hv,
			"continue_on_error": st.ContinueOnError, "retry_count": st.RetryCount,
			"retry_interval_sec": st.RetryIntervalSec,
		})
	}
	return out
}

func (h *orchHandler) Get(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	o, steps, svars, err := h.repo.Get(id)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询编排失败")
		return
	}
	if o == nil {
		resp.Fail(c, resp.CodeNotFound, "编排不存在")
		return
	}
	resp.OK(c, gin.H{"orchestration": o, "steps": h.buildStepsResponse(steps, svars)})
}

func (h *orchHandler) Save(c *gin.Context) {
	var req orchSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	o := &model.Orchestration{Name: req.Name, Description: req.Description, ExecMode: "by_step", Enabled: enabled}
	if id, _ := strconv.ParseInt(c.Param("id"), 10, 64); id > 0 {
		o.ID = id
	}

	steps := make([]model.OrchestrationStep, 0, len(req.Steps))
	svars := []model.OrchestrationStepVar{}
	for i, sr := range req.Steps {
		paramsJSON, _ := json.Marshal(sr.Params)
		scopeJSON, _ := json.Marshal(dedupInt64(sr.HostIDs))
		steps = append(steps, model.OrchestrationStep{
			TemplateID: sr.TemplateID, ParamsJSON: string(paramsJSON), HostScope: string(scopeJSON),
			ContinueOnError: sr.ContinueOnError, RetryCount: sr.RetryCount,
			RetryIntervalSec: orSec(sr.RetryIntervalSec),
		})
		seq := i + 1
		for hidStr, vars := range sr.HostVars {
			hid, err := strconv.ParseInt(hidStr, 10, 64)
			if err != nil || len(vars) == 0 {
				continue
			}
			vj, _ := json.Marshal(vars)
			svars = append(svars, model.OrchestrationStepVar{Seq: seq, HostID: hid, ParamsJSON: string(vj)})
		}
	}

	savedID, err := h.repo.Save(o, steps, svars)
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

func orSec(v int) int {
	if v <= 0 {
		return 30
	}
	return v
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

// Run POST /api/orchestrations/:id/run：创建运行并异步执行（顺序串行各步骤，长任务）。
func (h *orchHandler) Run(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	o, _, _, err := h.repo.Get(id)
	if err != nil || o == nil {
		resp.Fail(c, resp.CodeNotFound, "编排不存在")
		return
	}
	if !o.Enabled {
		resp.Fail(c, resp.CodeBadRequest, "编排已停用")
		return
	}
	go h.executeRun(id)
	h.auditRepo.Create(&model.AuditLog{Action: "orchestration.run", TargetType: "orchestration_run",
		Detail: fmt.Sprintf("name=%s", o.Name), RemoteIP: c.ClientIP()})
	resp.OK(c, gin.H{"started": true})
}

// orchProgress SSE 推送的运行事件。
type orchProgress struct {
	RunID     int64  `json:"run_id"`
	HostID    int64  `json:"host_id,omitempty"`
	Seq       int    `json:"seq,omitempty"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
	RunStatus string `json:"run_status,omitempty"`
}

// executeRun 引擎：步骤严格串行；步骤内主机并行（自适应并发）。
// 变量合并：模板默认 < 步骤默认 < 主机覆盖。某主机某步失败且未开「失败继续」时，
// 该主机不再参与后续步骤（明细置 skipped），其余主机不受影响 —— 长任务可安全跑完。
func (h *orchHandler) executeRun(orchID int64) {
	o, stepsDef, svars, err := h.repo.Get(orchID)
	if err != nil || o == nil || len(stepsDef) == 0 {
		return
	}
	override := map[int]map[int64]string{} // seq -> host -> paramsJSON
	for _, sv := range svars {
		if override[sv.Seq] == nil {
			override[sv.Seq] = map[int64]string{}
		}
		override[sv.Seq][sv.HostID] = sv.ParamsJSON
	}

	// 展开为运行明细（pending）
	runRows := make([]model.OrchestrationRunStep, 0)
	distinctHosts := map[int64]bool{}
	for _, def := range stepsDef {
		var hostIDs []int64
		_ = json.Unmarshal([]byte(def.HostScope), &hostIDs)
		for _, hid := range dedupInt64(hostIDs) {
			hh, herr := h.hostRepo.GetByID(hid)
			name, ip := "", ""
			if herr == nil && hh != nil {
				name, ip = hh.Name, hh.IP
			}
			distinctHosts[hid] = true
			tplName := ""
			if t, terr := h.tplRepo.GetTemplate(def.TemplateID); terr == nil && t != nil {
				tplName = t.Name
			}
			runRows = append(runRows, model.OrchestrationRunStep{HostID: hid, HostName: name,
				HostIP: ip, Seq: def.Seq, TemplateID: def.TemplateID, TemplateName: tplName})
		}
	}
	if len(runRows) == 0 {
		return
	}
	allIDs := make([]int64, 0, len(distinctHosts))
	for hid := range distinctHosts {
		allIDs = append(allIDs, hid)
	}
	tidsJSON, _ := json.Marshal(allIDs)
	run := &model.OrchestrationRun{OrchestrationID: orchID, Name: o.Name, ExecMode: "by_step",
		TotalHosts: len(distinctHosts), TriggerType: "manual", HostIDs: string(tidsJSON)}
	runID, err := h.repo.CreateRun(run, runRows)
	if err != nil {
		fmt.Printf("orchestration: 创建运行失败: %v\n", err)
		return
	}

	dead := map[int64]bool{} // 失败且未开「失败继续」的主机：不再参与后续步骤

	publish := func(hostID int64, seq int, status, output, errMsg string, attempt int) {
		if h.bus == nil {
			return
		}
		h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
			HostID: hostID, Seq: seq, Status: status, Output: output, Error: errMsg, Attempt: attempt, RunStatus: "running"})
	}

	execCell := func(cell store.RunStepRef, def *model.OrchestrationStep) {
		attempts := def.RetryCount + 1
		interval := def.RetryIntervalSec
		if interval <= 0 {
			interval = 30
		}
		var lastOut, lastErr string

		for a := 1; a <= attempts; a++ {
			_ = h.repo.UpdateRunStepStatus(cell.RecID, "running", a, "", "")
			publish(cell.HostID, cell.Seq, "running", "", "", a)

			tpl, terr := h.tplRepo.GetTemplate(def.TemplateID)
			if terr != nil || tpl == nil {
				lastErr = "模板不存在或已删除"
				break
			}
			// 变量合并：模板默认 < 步骤默认 < 主机覆盖
			stepParams := map[string]string{}
			_ = json.Unmarshal([]byte(def.ParamsJSON), &stepParams)
			hostParams := map[string]string{}
			if ov, ok := override[cell.Seq][cell.HostID]; ok {
				_ = json.Unmarshal([]byte(ov), &hostParams)
			}
			for k, v := range hostParams {
				stepParams[k] = v
			}
			rendered, rerr := renderScript(tpl.Script, tpl.Variables, stepParams)
			if rerr != nil {
				lastErr = rerr.Error()
				break // 渲染错误重试无意义
			}
			hr := store.HostRecord{DeployTaskHost: model.DeployTaskHost{
				HostID: cell.HostID, HostName: cell.HostName, HostIP: cell.HostIP}}
			rendered = applyHostVars(rendered, cell.Seq, hr)

			onLog := func(chunk string) {
				if chunk == "" || h.bus == nil {
					return
				}
				h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
					HostID: cell.HostID, Seq: cell.Seq, Status: "output", Output: chunk, Attempt: a, RunStatus: "running"})
			}
			out, execErr := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, cell.HostID, rendered, onLog)
			lastOut = out
			if execErr != nil {
				lastErr = execErr.Error()
				if a < attempts {
					time.Sleep(time.Duration(interval) * time.Second)
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
		_ = h.repo.UpdateRunStepStatus(cell.RecID, status, attempts, lastOut, lastErr)
		if status == "success" {
			tname := cell.TemplateName
			if t, _ := h.tplRepo.GetTemplate(def.TemplateID); t != nil {
				tname = t.Name
			}
			if err := h.tplRepo.MarkHostInstalled(cell.HostID, def.TemplateID, tname, runID); err != nil {
				fmt.Printf("orchestration: 标记安装失败 host=%d: %v\n", cell.HostID, err)
			}
		}
		publish(cell.HostID, cell.Seq, status, lastOut, lastErr, attempts)

		if status == "failed" && !def.ContinueOnError {
			dead[cell.HostID] = true
			_ = h.repo.SkipRemaining(runID, cell.HostID, cell.Seq)
			publish(cell.HostID, 0, "skipped-refresh", "", "", 0)
		}
	}

	allRows, err := h.repo.RunSteps(runID)
	if err != nil || len(allRows) == 0 {
		_, _ = h.repo.FinishRun(runID)
		return
	}

	concurrency := sysutil.AdaptiveConcurrency(len(distinctHosts))
	sem := make(chan struct{}, concurrency)

	// 步骤严格串行
	for i := range stepsDef {
		def := &stepsDef[i]
		var wg sync.WaitGroup
		for _, cell := range allRows {
			if cell.Seq != def.Seq || cell.Status == "skipped" || dead[cell.HostID] {
				continue
			}
			wg.Add(1)
			go func(cell store.RunStepRef, def *model.OrchestrationStep) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				execCell(cell, def)
			}(cell, def)
		}
		wg.Wait() // 本步骤全部主机终态后才进入下一步骤
	}

	finalStatus, _ := h.repo.FinishRun(runID)
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID, Status: "finished", RunStatus: finalStatus})
	}
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
