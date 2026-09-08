// 部署执行引擎与任务查询：批量 SSH 执行、进度事件推送。
package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store"
)

const (
	execTimeout = 600 * time.Second // 单台执行超时（安装类脚本耗时较长）
	outputLimit = 64 << 10          // 单台输出上限 64KB
)

// deployHandler 部署执行与任务查询。
type deployHandler struct {
	tplRepo   *store.DeployRepo
	schedRepo *store.DeployScheduleRepo
	hostRepo  *store.HostRepo
	credRepo  *store.CredentialRepo
	cryptoS   *icrypto.Service
	sshC      *sshx.Client
	bus       *eventbus.Bus
	auditRepo *store.AuditRepo
	sched     *deployScheduler
	conc      int // 执行并发数；<=0 表示按主机数自适应
}

// StartScheduler 启动定时任务调度器（随进程生命周期运行）。
func (h *deployHandler) StartScheduler() {
	h.sched = newScheduler(h)
	h.sched.start()
}

func NewDeployHandler(tplRepo *store.DeployRepo, schedRepo *store.DeployScheduleRepo, hostRepo *store.HostRepo,
	credRepo *store.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *store.AuditRepo, concurrency int) *deployHandler {
	return &deployHandler{tplRepo: tplRepo, schedRepo: schedRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo, conc: concurrency}
}

type runReq struct {
	TemplateID int64                       `json:"template_id" binding:"required"`
	HostIDs    []int64                     `json:"host_ids" binding:"required,min=1"`
	Params     map[string]string           `json:"params"`      // 任务级默认变量
	HostParams map[int64]map[string]string `json:"host_params"` // 主机级变量覆盖 host_id -> {k:v}
	Configs    map[string]string           `json:"configs"`     // 任务级自定义配置 config_key -> 内容（非空则覆盖默认配置文件）
	HostConfigs map[int64]map[string]string `json:"host_configs"` // 主机级自定义配置覆盖 host_id -> {key: content}
}

// deployProgress SSE 推送的进度事件。
type deployProgress struct {
	TaskID     int64  `json:"task_id"`
	HostID     int64  `json:"host_id"`
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	SuccessCnt int    `json:"success_cnt"`
	FailCnt    int    `json:"fail_cnt"`
	Total      int    `json:"total"`
	TaskStatus string `json:"task_status"` // running 或任务终态
}

// deployLogEvent 日志行事件（TopicDeployLogs，先落库再发布）。
type deployLogEvent struct {
	TaskID int64  `json:"task_id"`
	HostID int64  `json:"host_id"`
	HostIP string `json:"host_ip"`
	Text   string `json:"text"`
	ID     int64  `json:"id"`
	Ts     string `json:"ts,omitempty"`
}

// Run POST /api/deploy/run：创建任务并异步批量执行。
func (h *deployHandler) Run(c *gin.Context) {
	var req runReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	tpl, err := h.tplRepo.GetTemplate(req.TemplateID)
	if err != nil || tpl == nil {
		resp.Fail(c, resp.CodeNotFound, "模板不存在")
		return
	}
	taskID, err := h.createAndRun(tpl, req.HostIDs, req.Params, req.HostParams, req.Configs, req.HostConfigs, "manual", 0, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"task_id": taskID})
}

// createAndRun 校验渲染脚本、落库建任务并异步执行；手动与定时触发共用。
// 主机列表允许为空：任务照常创建并落执行记录（total=0）。
// hostParams 为逐主机变量覆盖（host_id -> {k:v}），为空则所有主机共用 params。
// configs/hostConfigs 为任务级/主机级自定义配置覆盖（config_key -> 内容），非空内容会在渲染期覆盖默认配置文件。
func (h *deployHandler) createAndRun(tpl *model.DeployTemplate, hostIDs []int64,
	params map[string]string, hostParams map[int64]map[string]string,
	taskConfigs map[string]string, hostConfigs map[int64]map[string]string,
	triggerType string, scheduleID int64, remoteIP string) (int64, error) {
	ids := dedupInt64(hostIDs)

	var hosts []model.DeployTaskHost
	for _, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			continue // 台账中已删除的主机自动跳过（定时触发容错）
		}
		// 合并变量（模板默认 < 任务默认 < 主机覆盖）并做渲染校验，提前暴露缺参
		merged, err := mergeParams(tpl.Variables, params, hostParams[id])
		if err != nil {
			return 0, err
		}
		// 合并自定义配置（任务级 < 主机覆盖），以保留键 __cfg.<key> 并入 ParamsJSON 持久化
		cfgMerged, err := mergeConfigs(tpl.Configs, taskConfigs, hostConfigs[id])
		if err != nil {
			return 0, fmt.Errorf("主机 %s 自定义配置校验失败: %w", hh.Name, err)
		}
		for k, v := range cfgMerged {
			merged["__cfg."+k] = v
		}
		if _, err := renderScript(tpl.Script, tpl.Variables, merged); err != nil {
			return 0, fmt.Errorf("主机 %s 变量校验失败: %w", hh.Name, err)
		}
		pb, _ := json.Marshal(merged)
		hosts = append(hosts, model.DeployTaskHost{
			HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP, Status: "pending",
			ParamsJSON: string(pb),
		})
	}

	taskParamsJSON, _ := json.Marshal(params)
	task := &model.DeployTask{
		TemplateID: tpl.ID, TemplateName: tpl.Name, Total: len(hosts),
		TriggerType: triggerType, ScheduleID: scheduleID, ParamsJSON: string(taskParamsJSON),
	}
	taskID, err := h.tplRepo.CreateTask(task, hosts)
	if err != nil {
		return 0, fmt.Errorf("创建任务失败: %w", err)
	}

	detail := fmt.Sprintf("template=%s hosts=%d", tpl.Name, len(hosts))
	if triggerType == "schedule" {
		detail += fmt.Sprintf(" trigger=schedule:%d", scheduleID)
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: "deploy.run", TargetType: "deploy_task", TargetID: taskID,
		Detail: detail, RemoteIP: remoteIP,
	})

	go h.execute(taskID)
	return taskID, nil
}

// mergeParams 合并变量：模板默认值 < 任务级默认 < 主机级覆盖。
func mergeParams(rawVars json.RawMessage, taskParams, hostParams map[string]string) (map[string]string, error) {
	vars, err := parseVariables(rawVars)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string, len(vars))
	for _, v := range vars {
		if v.Default != "" {
			merged[v.Name] = v.Default
		}
	}
	for k, val := range taskParams {
		merged[k] = val
	}
	for k, val := range hostParams {
		merged[k] = val
	}
	return merged, nil
}

// parseConfigs 解析模板 configs 声明。
func parseConfigs(raw json.RawMessage) ([]model.TemplateConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var cfgs []model.TemplateConfig
	if err := json.Unmarshal(raw, &cfgs); err != nil {
		return nil, err
	}
	return cfgs, nil
}

// mergeConfigs 合并自定义配置：任务级 < 主机级覆盖。模板声明 required 且结果为空时报错。
func mergeConfigs(rawConfigs json.RawMessage, taskConfigs, hostConfigs map[string]string) (map[string]string, error) {
	cfgs, err := parseConfigs(rawConfigs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(cfgs))
	for _, c := range cfgs {
		if c.Key == "" {
			continue
		}
		v, ok := hostConfigs[c.Key]
		if !ok {
			v, ok = taskConfigs[c.Key]
		}
		if !ok || strings.TrimSpace(v) == "" {
			if c.Required {
				return nil, fmt.Errorf("缺少必填自定义配置: %s(%s)", c.Label, c.Key)
			}
			continue
		}
		out[c.Key] = v
	}
	return out, nil
}

// applyConfigOverrides 渲染期覆盖默认配置文件：对提供了非空自定义内容的 config，
// 用 base64 解码安全写入其目标文件。脚本中须用锚点
// # __DEPLOY_CONF__ <key> … # __DEPLOY_CONF_END__ <key> 包裹默认写入块；无锚点则忽略并告警。
func applyConfigOverrides(script string, tpl *model.DeployTemplate, merged map[string]string) string {
	cfgs, err := parseConfigs(tpl.Configs)
	if err != nil || len(cfgs) == 0 {
		return script
	}
	out := script
	for _, c := range cfgs {
		if c.Key == "" {
			continue
		}
		content, ok := merged["__cfg."+c.Key]
		if !ok || strings.TrimSpace(content) == "" {
			continue // 未提供 → 保持脚本默认
		}
		beginTag := "# __DEPLOY_CONF__ " + c.Key
		endTag := "# __DEPLOY_CONF_END__ " + c.Key
		b := strings.Index(out, beginTag)
		if b < 0 {
			log.Printf("deploy: 模板 config %s(%s) 未在脚本中找到锚点，自定义配置已忽略", c.Key, c.Label)
			continue
		}
		e := strings.Index(out[b+len(beginTag):], endTag)
		if e < 0 {
			continue
		}
		ePos := b + len(beginTag) + e + len(endTag)
		file := renderConfigFile(c.File, merged)
		b64 := base64.StdEncoding.EncodeToString([]byte(content))
		quoted := strings.ReplaceAll(file, "'", "'\\''")
		injected := beginTag + "\n" +
			"# 用户自定义配置覆盖：" + c.Label + "\n" +
			"printf '%s\\n' \"$(printf '%s' '" + b64 + "' | base64 -d)\" > '" + quoted + "'\n" +
			endTag + "\n"
		out = out[:b] + injected + out[ePos:]
	}
	return out
}

// renderConfigFile 渲染 config.file 中的 {{var}} 占位（跳过 __cfg.* 保留键）。
func renderConfigFile(f string, merged map[string]string) string {
	for k, v := range merged {
		if strings.HasPrefix(k, "__cfg.") {
			continue
		}
		f = strings.ReplaceAll(f, "{{"+k+"}}", v)
	}
	return f
}

// Tasks GET /api/deploy/tasks
func (h *deployHandler) Tasks(c *gin.Context) {
	page, pageSize := parsePage(c)
	items, total, err := h.tplRepo.ListTasks(page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询任务失败")
		return
	}
	if items == nil {
		items = []model.DeployTask{}
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

// TaskDetail GET /api/deploy/tasks/:id
func (h *deployHandler) TaskDetail(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	task, err := h.tplRepo.GetTask(id)
	if err != nil || task == nil {
		resp.Fail(c, resp.CodeNotFound, "任务不存在")
		return
	}
	records, _ := h.tplRepo.TaskHosts(id)
	hosts := make([]model.DeployTaskHost, 0, len(records))
	for _, r := range records {
		hosts = append(hosts, r.DeployTaskHost)
	}
	resp.OK(c, gin.H{
		"id": task.ID, "template_id": task.TemplateID, "template_name": task.TemplateName,
		"status": task.Status, "total": task.Total, "success_cnt": task.SuccessCnt,
		"fail_cnt": task.FailCnt, "created_at": task.CreatedAt, "finished_at": task.FinishedAt,
		"hosts": hosts,
	})
}

// SSEProgress GET /api/sse/deploy?task_id=N：单任务的实时进度流。
func (h *deployHandler) SSEProgress(c *gin.Context) {
	taskID, err := strconv.ParseInt(c.Query("task_id"), 10, 64)
	if err != nil || taskID <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "task_id 无效")
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

	ch := make(chan deployProgress, 64)
	var subID int
	if h.bus != nil {
		subID = h.bus.Subscribe(eventbus.TopicDeployProgress, func(ev eventbus.Event) {
			if p, ok := ev.Data.(deployProgress); ok && p.TaskID == taskID {
				select {
				case ch <- p:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicDeployProgress, subID)
	}

	c.SSEvent("connected", taskID)
	flusher.Flush()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case p := <-ch:
			data, _ := json.Marshal(p)
			if p.TaskStatus != "running" {
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

// execute 并发执行任务内全部主机，逐台按各自变量渲染并发布进度事件，结束后落终态。
func (h *deployHandler) execute(taskID int64) {
	records, err := h.tplRepo.TaskHosts(taskID)
	if err != nil || len(records) == 0 {
		_, _ = h.tplRepo.FinishTask(taskID)
		return
	}
	task, _ := h.tplRepo.GetTask(taskID) // 用于成功后写入主机安装标记
	tpl, _ := h.tplRepo.GetTemplate(task.TemplateID)

	var mu sync.Mutex
	successCnt, failCnt := 0, 0
	publish := func(rec store.HostRecord, status, output, errMsg string) {
		mu.Lock()
		switch status {
		case "success":
			successCnt++
		case "failed":
			failCnt++
		}
		p := deployProgress{TaskID: taskID, HostID: rec.HostID, Status: status,
			Output: output, Error: errMsg, SuccessCnt: successCnt, FailCnt: failCnt,
			Total: len(records), TaskStatus: "running"}
		mu.Unlock()

		if status == "success" && task != nil {
			if err := h.tplRepo.MarkHostInstalled(rec.HostID, task.TemplateID, task.TemplateName, taskID); err != nil {
				log.Printf("deploy: 标记安装记录失败 host=%d: %v", rec.HostID, err)
			}
			vars := map[string]string{}
			_ = json.Unmarshal([]byte(rec.ParamsJSON), &vars)
			if err := registerTemplateServices(h.tplRepo, rec.HostID, rec.HostIP, task.TemplateID, vars); err != nil {
				log.Printf("deploy: 登记服务失败 host=%d: %v", rec.HostID, err)
			}
		}
		_ = h.tplRepo.UpdateHostStatus(rec.RecID, status, output, errMsg)
		if h.bus != nil {
			h.bus.Publish(eventbus.TopicDeployProgress, p)
		}
	}

	concurrency := h.conc
	if concurrency <= 0 {
		concurrency = sysutil.AdaptiveConcurrency(len(records)) // 按本轮主机数自适应
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range records {
		wg.Add(1)
		go func(rec store.HostRecord, seq int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// 先发布 running 事件，避免长任务执行期间前端一直显示"等待中"
			if h.bus != nil {
				h.bus.Publish(eventbus.TopicDeployProgress, deployProgress{
					TaskID: taskID, HostID: rec.HostID, Status: "running", Total: len(records), TaskStatus: "running",
				})
			}

			// 执行过程中增量日志：拆行落库后再发布（日志抽屉 init 快照回放 + 实时追加）
			onLog := func(chunk string) {
				if chunk == "" {
					return
				}
				lines := splitLogLines(chunk)
				if len(lines) == 0 {
					return
				}
				rows := make([]model.DeployLog, 0, len(lines))
				for _, ln := range lines {
					rows = append(rows, model.DeployLog{HostID: rec.HostID, HostIP: rec.HostIP, Text: ln})
				}
				persisted, err := h.tplRepo.AppendTaskLogs(taskID, rows)
				if err != nil {
					log.Printf("deploy: 写日志失败 task=%d host=%d: %v", taskID, rec.HostID, err)
					return
				}
				if h.bus != nil {
					for _, l := range persisted {
						h.bus.Publish(eventbus.TopicDeployLogs, deployLogEvent{
							TaskID: taskID, HostID: l.HostID, HostIP: l.HostIP,
							Text: l.Text, ID: l.ID, Ts: l.CreatedAt,
						})
					}
				}
			}

			// 按本主机变量覆盖渲染（模板默认 < 任务默认 < 主机覆盖）
			if tpl == nil {
				publish(rec, "failed", "", "模板不存在或已删除")
				return
			}
			var params map[string]string
			_ = json.Unmarshal([]byte(rec.ParamsJSON), &params)
			rendered, rerr := renderScript(tpl.Script, tpl.Variables, params)
			// 自定义配置覆盖：若用户提供了非空内容，渲染期替换默认写入块
			rendered = applyConfigOverrides(rendered, tpl, params)
			if rerr != nil {
				publish(rec, "failed", "", "脚本渲染失败: "+rerr.Error())
				return
			}
			rendered = applyHostVars(rendered, seq, rec)

			// 前置依赖检查：不满足则该主机直接失败，不执行主脚本
			if hint := checkRequires(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, rec.HostID, templateRequires(tpl)); hint != "" {
				publish(rec, "failed", "", "前置依赖不满足："+hint)
				return
			}
			h.appendLog(taskID, rec.HostID, rec.HostIP, "开始执行")
			output, execErr := h.execOnHost(rec.HostID, rendered, onLog)
			status, errMsg := "success", ""
			if execErr != nil {
				status, errMsg = "failed", execErr.Error()
			}
			// 成功任务支持脚本自报：infra-ops:set-name=xxx 自动同步平台台账主机名
			if status == "success" {
				if newName := extractSelfReportedName(output); newName != "" {
					if err := h.hostRepo.Rename(rec.HostID, newName); err != nil {
						log.Printf("deploy: 同步主机名失败 host=%d name=%s: %v", rec.HostID, newName, err)
					} else {
						rec.HostName = newName
					}
				}
			}
			if status == "success" {
				h.appendLog(taskID, rec.HostID, rec.HostIP, "执行成功")
			} else {
				h.appendLog(taskID, rec.HostID, rec.HostIP, "执行失败："+errMsg)
			}
			publish(rec, status, output, errMsg)
		}(records[i], i+1)
	}
	wg.Wait()

	finalStatus, _ := h.tplRepo.FinishTask(taskID)
	mu.Lock()
	sc, fc := successCnt, failCnt
	mu.Unlock()
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicDeployProgress, deployProgress{
			TaskID: taskID, Status: "finished", SuccessCnt: sc, FailCnt: fc,
			Total: len(records), TaskStatus: finalStatus,
		})
	}
}

// appendLog 写一条状态日志行：先落库再发布（与 onLog 共用日志抽屉闭环）。
func (h *deployHandler) appendLog(taskID, hostID int64, hostIP, text string) {
	persisted, err := h.tplRepo.AppendTaskLogs(taskID, []model.DeployLog{{HostID: hostID, HostIP: hostIP, Text: text}})
	if err != nil {
		log.Printf("deploy: 写日志失败 task=%d host=%d: %v", taskID, hostID, err)
		return
	}
	if h.bus != nil {
		for _, l := range persisted {
			h.bus.Publish(eventbus.TopicDeployLogs, deployLogEvent{
				TaskID: taskID, HostID: l.HostID, HostIP: l.HostIP,
				Text: l.Text, ID: l.ID, Ts: l.CreatedAt,
			})
		}
	}
}

// SSESetup GET /api/sse/deploy/setup：执行记录常驻状态流（不传 task_id）。
// 连接时查询是否存在运行中任务：有则带快照并按主机粒度进度事件转发（运行→终态）；
// 无则只发 idle 并保持连接；常驻监听下，之后任意新启动的任务也会被实时转发。
func (h *deployHandler) SSESetup(c *gin.Context) {
	flusher, ok := sseSetup(c)
	if !ok {
		return
	}

	running, _ := h.tplRepo.ListRunningTasks()
	snap := make([]gin.H, 0, len(running))
	for _, t := range running {
		snap = append(snap, gin.H{"task_id": t.ID, "template_name": t.TemplateName, "status": t.Status,
			"total": t.Total, "success_cnt": t.SuccessCnt, "fail_cnt": t.FailCnt})
	}
	c.SSEvent("init", gin.H{"idle": len(snap) == 0, "running": snap})
	flusher.Flush()

	ch := make(chan deployProgress, 128)
	if h.bus != nil {
		subID := h.bus.Subscribe(eventbus.TopicDeployProgress, func(ev eventbus.Event) {
			if p, ok := ev.Data.(deployProgress); ok {
				select {
				case ch <- p:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicDeployProgress, subID)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case p := <-ch:
			if p.TaskStatus != "running" { // 任务终态
				c.SSEvent("done", gin.H{"task_id": p.TaskID, "status": p.TaskStatus,
					"success_cnt": p.SuccessCnt, "fail_cnt": p.FailCnt, "total": p.Total})
			} else { // 主机级进度增量（含新任务开始的首个 running 事件）
				c.SSEvent("track", gin.H{"task_id": p.TaskID, "status": p.Status,
					"success_cnt": p.SuccessCnt, "fail_cnt": p.FailCnt, "total": p.Total})
			}
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		case <-c.Request.Context().Done():
			return false
		}
		flusher.Flush()
		return true
	})
}

// SSELog GET /api/sse/deploy/log?task_id=N：执行记录日志流。
// 事件：init（快照：已落库日志最近 2000 行 + 任务状态）→ log（实时行）→ done（结束关闭）。已结束任务仅 init+done 回溯。
func (h *deployHandler) SSELog(c *gin.Context) {
	taskID, err := strconv.ParseInt(c.Query("task_id"), 10, 64)
	if err != nil || taskID <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "task_id 无效")
		return
	}
	flusher, ok := sseSetup(c)
	if !ok {
		return
	}

	taskStatus := "running"
	if t, _ := h.tplRepo.GetTask(taskID); t != nil {
		taskStatus = t.Status
	}
	logs, _ := h.tplRepo.TaskLogs(taskID)
	ls := make([]gin.H, 0, len(logs))
	for _, l := range logs {
		ls = append(ls, gin.H{"id": l.ID, "ts": l.CreatedAt, "ip": l.HostIP, "text": l.Text})
	}
	c.SSEvent("init", gin.H{"task_id": taskID, "task_status": taskStatus, "logs": ls})
	flusher.Flush()

	if taskStatus != "running" { // 已结束任务：init → done 立即关闭（回溯语义）
		c.SSEvent("done", gin.H{"task_status": taskStatus})
		flusher.Flush()
		return
	}

	ch := make(chan deployLogEvent, 256)
	doneCh := make(chan string, 4) // 任务终态信号，收到即关闭日志流
	if h.bus != nil {
		subLog := h.bus.Subscribe(eventbus.TopicDeployLogs, func(ev eventbus.Event) {
			if l, ok := ev.Data.(deployLogEvent); ok && l.TaskID == taskID {
				select {
				case ch <- l:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicDeployLogs, subLog)
		// 任务终态走进度总线：同时监听，保证日志流能感知任务从运行中→已完成/失败
		subProg := h.bus.Subscribe(eventbus.TopicDeployProgress, func(ev eventbus.Event) {
			if p, ok := ev.Data.(deployProgress); ok && p.TaskID == taskID && p.TaskStatus != "running" {
				select {
				case doneCh <- p.TaskStatus:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicDeployProgress, subProg)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case l := <-ch:
			c.SSEvent("log", gin.H{"id": l.ID, "ts": l.Ts, "ip": l.HostIP, "text": l.Text})
		case ts := <-doneCh:
			c.SSEvent("done", gin.H{"task_status": ts})
			flusher.Flush()
			return false
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		case <-c.Request.Context().Done():
			return false
		}
		flusher.Flush()
		return true
	})
}

// execOnHost 解密凭据→SSH 拨号→执行渲染后脚本；onLog 在执行过程中接收增量输出。
func (h *deployHandler) execOnHost(hostID int64, script string, onLog func(string)) (string, error) {
	return execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hostID, script, onLog)
}

// templateRequires 解析模板前置依赖声明。
func templateRequires(t *model.DeployTemplate) []model.TemplateDependency {
	if t == nil || len(t.Requires) == 0 {
		return nil
	}
	var deps []model.TemplateDependency
	if err := json.Unmarshal(t.Requires, &deps); err != nil {
		return nil
	}
	return deps
}

// checkRequires 对单主机逐条执行前置依赖检查；全部通过返回空串，否则返回阻断提示。
func checkRequires(hostRepo *store.HostRepo, credRepo *store.CredentialRepo, cryptoS *icrypto.Service,
	sshC *sshx.Client, hostID int64, reqs []model.TemplateDependency) string {
	for _, d := range reqs {
		if strings.TrimSpace(d.Check) == "" {
			continue
		}
		if _, err := execHostWith(hostRepo, credRepo, cryptoS, sshC, hostID, d.Check, nil); err != nil {
			if d.Hint != "" {
				return d.Hint
			}
			return "依赖检查未通过：" + d.Check
		}
	}
	return ""
}

// execHostWith 部署与编排共用的单主机执行：解密凭据→SSH 拨号→运行脚本。
func execHostWith(hostRepo *store.HostRepo, credRepo *store.CredentialRepo, cryptoS *icrypto.Service,
	sshC *sshx.Client, hostID int64, script string, onLog func(string)) (string, error) {
	host, err := hostRepo.GetByID(hostID)
	if err != nil || host == nil {
		return "", fmt.Errorf("主机不存在")
	}
	cred, err := credRepo.GetByID(host.CredentialID)
	if err != nil || cred == nil {
		return "", fmt.Errorf("凭据不存在")
	}
	secret, err := cryptoS.Decrypt(cred.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("凭据解密失败: %w", err)
	}

	dialCfg := sshx.DialConfig{
		Addr:     fmt.Sprintf("%s:%d", host.IP, host.Port),
		Username: cred.Username,
	}
	if cred.Type == "private_key" {
		dialCfg.PrivateKey = secret
	} else {
		dialCfg.Password = string(secret)
	}
	client, err := sshC.Dial(dialCfg)
	if err != nil {
		return "", err
	}
	defer client.Close()

	return runRemoteScript(client, script, onLog)
}

// runRemoteScript 在连接上执行脚本：合并输出落缓冲（上限 64KB），
// 同时按 ~400ms 节流把增量输出回调给 onLog（SSE 实时日志），超时 600s。
func runRemoteScript(client *ssh.Client, script string, onLog func(string)) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	tw := &streamTee{onLog: onLog}
	session.Stdout = tw
	session.Stderr = tw

	done := make(chan error, 1)
	go func() { done <- session.Run(script) }()

	flushTicker := time.NewTicker(400 * time.Millisecond)
	stop := make(chan struct{})
	defer func() { close(stop); flushTicker.Stop() }()
	go func() {
		for {
			select {
			case <-flushTicker.C:
				tw.Flush()
			case <-stop:
				return
			}
		}
	}()

	var execErr error
	select {
	case execErr = <-done:
		tw.Flush() // 收尾冲刷残余输出
	case <-time.After(execTimeout):
		_ = session.Close()
		tw.Flush()
		return tw.Snapshot(), fmt.Errorf("执行超时(%s)", execTimeout)
	}
	if execErr != nil {
		return tw.Snapshot(), fmt.Errorf("exit: %w", execErr)
	}
	return tw.Snapshot(), nil
}

// streamTee 把 SSH 输出同时写入全量快照与待发送增量区；Flush 由节流器周期调用。
type streamTee struct {
	mu      sync.Mutex
	all     limitedBuffer // 全量快照，最终落库
	pending []byte        // 待推送增量
	onLog   func(string)
}

func (w *streamTee) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.all.Write(p)
	// 待发送区将溢出时先同步冲刷一次，保证输出顺序
	if len(w.pending)+len(p) > 16<<10 && len(w.pending) > 0 && w.onLog != nil {
		w.onLog(string(w.pending))
		w.pending = nil
	}
	if remain := (16 << 10) - len(w.pending); len(p) > remain {
		p = p[:remain]
	}
	w.pending = append(w.pending, p...)
	return len(p), nil
}

// Flush 把当前累积的增量输出推送给回调。
func (w *streamTee) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 || w.onLog == nil {
		return
	}
	w.onLog(string(w.pending))
	w.pending = nil
}

// Snapshot 返回全量输出快照。
func (w *streamTee) Snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.all.String()
}

// limitedBuffer 带写入上限的缓冲，防止超长输出撑爆内存。
type limitedBuffer struct{ b []byte }

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if remain := outputLimit - len(w.b); remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		w.b = append(w.b, p...)
	}
	return len(p), nil
}

func (w *limitedBuffer) String() string { return string(w.b) }

// setNameMarkerRE 脚本自报主机名约定行：infra-ops:set-name=新名字（取最后一次出现）。
var setNameMarkerRE = regexp.MustCompile(`(?m)^\s*infra-ops:set-name=(\S[^\r\n]*)$`)

// extractSelfReportedName 从脚本输出中提取自报的新主机名；无则返回空串。
func extractSelfReportedName(output string) string {
	matches := setNameMarkerRE.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return ""
	}
	name := strings.TrimSpace(matches[len(matches)-1][1])
	name = strings.Trim(name, "\"'")
	if name == "" || len(name) > 64 {
		return ""
	}
	return name
}

// applyHostVars 替换内置主机变量：{{__seq}} 任务内序号（1 起）、
// {{__ip}} 主机 IP、{{__ip_last}} IP 末段、{{__name}} 当前主机名。
func applyHostVars(script string, seq int, rec store.HostRecord) string {
	return strings.NewReplacer(
		"{{__seq}}", strconv.Itoa(seq),
		"{{__ip}}", rec.HostIP,
		"{{__ip_last}}", lastOctet(rec.HostIP),
		"{{__name}}", rec.HostName,
	).Replace(script)
}

// lastOctet 取点分 IPv4 的末段；非标准格式原样返回。
func lastOctet(ip string) string {
	if i := strings.LastIndexByte(ip, '.'); i >= 0 {
		return ip[i+1:]
	}
	return ip
}

func dedupInt64(in []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
