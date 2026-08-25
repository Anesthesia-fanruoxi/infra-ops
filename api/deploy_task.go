// 部署执行引擎与任务查询：批量 SSH 执行、进度事件推送。
package api

import (
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
	TemplateID int64                        `json:"template_id" binding:"required"`
	HostIDs    []int64                      `json:"host_ids" binding:"required,min=1"`
	Params     map[string]string            `json:"params"`      // 任务级默认变量
	HostParams map[int64]map[string]string  `json:"host_params"` // 主机级变量覆盖 host_id -> {k:v}
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
	taskID, err := h.createAndRun(tpl, req.HostIDs, req.Params, req.HostParams, "manual", 0, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"task_id": taskID})
}

// createAndRun 校验渲染脚本、落库建任务并异步执行；手动与定时触发共用。
// 主机列表允许为空：任务照常创建并落执行记录（total=0）。
// hostParams 为逐主机变量覆盖（host_id -> {k:v}），为空则所有主机共用 params。
func (h *deployHandler) createAndRun(tpl *model.DeployTemplate, hostIDs []int64,
	params map[string]string, hostParams map[int64]map[string]string, triggerType string, scheduleID int64, remoteIP string) (int64, error) {
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

			// 执行过程中增量推送日志片段（SSE 实时输出）
			onLog := func(chunk string) {
				if chunk == "" || h.bus == nil {
					return
				}
				h.bus.Publish(eventbus.TopicDeployProgress, deployProgress{
					TaskID: taskID, HostID: rec.HostID, Status: "output",
					Output: chunk, Total: len(records), TaskStatus: "running",
				})
			}

			// 按本主机变量覆盖渲染（模板默认 < 任务默认 < 主机覆盖）
			if tpl == nil {
				publish(rec, "failed", "", "模板不存在或已删除")
				return
			}
			var params map[string]string
			_ = json.Unmarshal([]byte(rec.ParamsJSON), &params)
			rendered, rerr := renderScript(tpl.Script, tpl.Variables, params)
			if rerr != nil {
				publish(rec, "failed", "", "脚本渲染失败: "+rerr.Error())
				return
			}
			rendered = applyHostVars(rendered, seq, rec)

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

// execOnHost 解密凭据→SSH 拨号→执行渲染后脚本；onLog 在执行过程中接收增量输出。
func (h *deployHandler) execOnHost(hostID int64, script string, onLog func(string)) (string, error) {
	return execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hostID, script, onLog)
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
