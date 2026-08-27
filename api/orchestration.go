// 任务编排：编排 = 多个「部署单元」（模板+该步骤主机集+逐台变量）按顺序串行的长任务。
package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
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
	logRepo   *store.OrchestrationLogRepo
}

func NewOrchHandler(repo *store.OrchestrationRepo, tplRepo *store.DeployRepo, hostRepo *store.HostRepo,
	credRepo *store.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *store.AuditRepo, logRepo *store.OrchestrationLogRepo) *orchHandler {
	return &orchHandler{repo: repo, tplRepo: tplRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo, logRepo: logRepo}
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
	Name        string        `json:"name" binding:"required"`
	Description string        `json:"description"`
	Enabled     *bool         `json:"enabled"`
	Steps       []orchStepReq `json:"steps" binding:"required,min=1,dive"`
}

func (h *orchHandler) List(c *gin.Context) {
	// state 可选：running / not_started / finished；不带参数返回全部。
	items, err := h.repo.List(c.Query("state"))
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
		hasRun, herr := h.repo.HasRun(id)
		if herr != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "查询任务记录失败")
			return
		}
		if hasRun {
			resp.Fail(c, resp.CodeBadRequest, "已开始执行的任务记录不可编辑")
			return
		}
	}

	steps := make([]model.OrchestrationStep, 0, len(req.Steps))
	svars := []model.OrchestrationStepVar{}
	for i, sr := range req.Steps {
		paramsJSON := mustJSONObj(sr.Params)
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

// mustJSONObj 序列化对象；nil 输出 "{}"（json.Marshal(nil map) 会得到 "null"，反序列化后为 nil map 导致赋值 panic）。
func mustJSONObj(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil || string(b) == "null" {
		return "{}"
	}
	return string(b)
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
	runID, err := h.createRun(id)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	go h.executeRun(id, runID)
	h.auditRepo.Create(&model.AuditLog{Action: "orchestration.run", TargetType: "orchestration_run",
		Detail: fmt.Sprintf("orchestration_id=%d run_id=%d", id, runID), RemoteIP: c.ClientIP()})
	resp.OK(c, gin.H{"started": true, "run_id": runID})
}

// createRun 同步创建运行记录（含 pending 步骤），返回 runID。
// 同步创建便于前端立即拿到 run_id 订阅 SSE 进度；真正的执行在 goroutine 中异步进行。
func (h *orchHandler) createRun(orchID int64) (int64, error) {
	o, stepsDef, svars, err := h.repo.Get(orchID)
	if err != nil || o == nil {
		return 0, fmt.Errorf("编排不存在")
	}
	if !o.Enabled {
		return 0, fmt.Errorf("编排已停用")
	}
	hasRun, herr := h.repo.HasRun(orchID)
	if herr != nil {
		return 0, fmt.Errorf("查询运行记录失败")
	}
	if hasRun {
		return 0, fmt.Errorf("该任务记录已执行过，不可重复执行")
	}
	if len(stepsDef) == 0 {
		return 0, fmt.Errorf("编排没有步骤")
	}
	override := map[int]map[int64]string{}
	for _, sv := range svars {
		if override[sv.Seq] == nil {
			override[sv.Seq] = map[int64]string{}
		}
		override[sv.Seq][sv.HostID] = sv.ParamsJSON
	}
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
		return 0, fmt.Errorf("没有可执行的步骤（请为步骤选择主机）")
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
		return 0, fmt.Errorf("创建运行失败: %v", err)
	}
	return runID, nil
}

// ---------- 运行明细（RunsDetail 见 orchestration_sse.go，与运行详情/抽屉同域） ----------
