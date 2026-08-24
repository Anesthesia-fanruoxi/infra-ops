// 部署执行引擎与任务查询：批量 SSH 执行、进度事件推送。
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store"
)

const (
	execTimeout   = 600 * time.Second // 单台执行超时（安装类脚本耗时较长）
	outputLimit   = 64 << 10          // 单台输出上限 64KB
	runConcurrent = 5                 // 并发执行数
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
}

// StartScheduler 启动定时任务调度器（随进程生命周期运行）。
func (h *deployHandler) StartScheduler() {
	h.sched = newScheduler(h)
	h.sched.start()
}

func NewDeployHandler(tplRepo *store.DeployRepo, schedRepo *store.DeployScheduleRepo, hostRepo *store.HostRepo,
	credRepo *store.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *store.AuditRepo) *deployHandler {
	return &deployHandler{tplRepo: tplRepo, schedRepo: schedRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo}
}

type runReq struct {
	TemplateID int64             `json:"template_id" binding:"required"`
	HostIDs    []int64           `json:"host_ids" binding:"required,min=1"`
	Params     map[string]string `json:"params"`
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
	taskID, err := h.createAndRun(tpl, req.HostIDs, req.Params, "manual", 0, c.ClientIP())
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	resp.OK(c, gin.H{"task_id": taskID})
}

// createAndRun 校验渲染脚本、落库建任务并异步执行；手动与定时触发共用。
// 主机列表允许为空：任务照常创建并落执行记录（total=0）。
func (h *deployHandler) createAndRun(tpl *model.DeployTemplate, hostIDs []int64,
	params map[string]string, triggerType string, scheduleID int64, remoteIP string) (int64, error) {
	ids := dedupInt64(hostIDs)
	script, err := renderScript(tpl.Script, tpl.Variables, params)
	if err != nil {
		return 0, err
	}

	var hosts []model.DeployTaskHost
	for _, id := range ids {
		hh, err := h.hostRepo.GetByID(id)
		if err != nil || hh == nil {
			continue // 台账中已删除的主机自动跳过（定时触发容错）
		}
		hosts = append(hosts, model.DeployTaskHost{
			HostID: hh.ID, HostName: hh.Name, HostIP: hh.IP, Status: "pending",
		})
	}

	task := &model.DeployTask{
		TemplateID: tpl.ID, TemplateName: tpl.Name, Total: len(hosts),
		TriggerType: triggerType, ScheduleID: scheduleID,
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

	go h.execute(taskID, script)
	return taskID, nil
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

// execute 并发执行任务内全部主机，逐台发布进度事件，结束后落终态。
func (h *deployHandler) execute(taskID int64, script string) {
	records, err := h.tplRepo.TaskHosts(taskID)
	if err != nil || len(records) == 0 {
		_, _ = h.tplRepo.FinishTask(taskID)
		return
	}

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

		_ = h.tplRepo.UpdateHostStatus(rec.RecID, status, output, errMsg)
		if h.bus != nil {
			h.bus.Publish(eventbus.TopicDeployProgress, p)
		}
	}

	sem := make(chan struct{}, runConcurrent)
	var wg sync.WaitGroup
	for _, rec := range records {
		wg.Add(1)
		go func(rec store.HostRecord) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// 先发布 running 事件，避免长任务执行期间前端一直显示"等待中"
			if h.bus != nil {
				h.bus.Publish(eventbus.TopicDeployProgress, deployProgress{
					TaskID: taskID, HostID: rec.HostID, Status: "running", Total: len(records), TaskStatus: "running",
				})
			}

			output, execErr := h.execOnHost(rec.HostID, script)
			status, errMsg := "success", ""
			if execErr != nil {
				status, errMsg = "failed", execErr.Error()
			}
			publish(rec, status, output, errMsg)
		}(rec)
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

// execOnHost 解密凭据→SSH 拨号→执行渲染后脚本，返回合并输出。
func (h *deployHandler) execOnHost(hostID int64, script string) (string, error) {
	host, err := h.hostRepo.GetByID(hostID)
	if err != nil || host == nil {
		return "", fmt.Errorf("主机不存在")
	}
	cred, err := h.credRepo.GetByID(host.CredentialID)
	if err != nil || cred == nil {
		return "", fmt.Errorf("凭据不存在")
	}
	secret, err := h.cryptoS.Decrypt(cred.EncryptedSecret)
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
	client, err := h.sshC.Dial(dialCfg)
	if err != nil {
		return "", err
	}
	defer client.Close()

	return runRemoteScript(client, script)
}

// runRemoteScript 在连接上执行脚本，捕获合并输出（上限 64KB），超时 300s。
func runRemoteScript(client *ssh.Client, script string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var buf limitedBuffer
	session.Stdout = &buf
	session.Stderr = &buf

	done := make(chan error, 1)
	go func() { done <- session.Run(script) }()

	select {
	case err := <-done:
		if err != nil {
			return buf.String(), fmt.Errorf("exit: %w", err)
		}
		return buf.String(), nil
	case <-time.After(execTimeout):
		_ = session.Close()
		return buf.String(), fmt.Errorf("执行超时(%s)", execTimeout)
	}
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
