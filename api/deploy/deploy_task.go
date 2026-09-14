// 部署执行引擎与任务查询：批量 SSH 执行、进度事件推送。
package deploy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	icrypto "infra-ops/common/crypto"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store/repo"
)

const (
	execTimeout = 600 * time.Second // 单台执行超时（安装类脚本耗时较长）
	outputLimit = 64 << 10          // 单台输出上限 64KB
)

// deployHandler 部署执行与任务查询。
type deployHandler struct {
	tplRepo   *repo.DeployRepo
	schedRepo *repo.DeployScheduleRepo
	hostRepo  *repo.HostRepo
	credRepo  *repo.CredentialRepo
	cryptoS   *icrypto.Service
	sshC      *sshx.Client
	bus       *eventbus.Bus
	auditRepo *repo.AuditRepo
	sched     *deployScheduler
	conc      int // 执行并发数；<=0 表示按主机数自适应
}

// StartScheduler 启动定时任务调度器（随进程生命周期运行）。
func (h *deployHandler) StartScheduler() {
	h.sched = newScheduler(h)
	h.sched.start()
}

func NewDeployHandler(tplRepo *repo.DeployRepo, schedRepo *repo.DeployScheduleRepo, hostRepo *repo.HostRepo,
	credRepo *repo.CredentialRepo, cryptoS *icrypto.Service, sshC *sshx.Client,
	bus *eventbus.Bus, auditRepo *repo.AuditRepo, concurrency int) *deployHandler {
	return &deployHandler{tplRepo: tplRepo, schedRepo: schedRepo, hostRepo: hostRepo, credRepo: credRepo,
		cryptoS: cryptoS, sshC: sshC, bus: bus, auditRepo: auditRepo, conc: concurrency}
}

type runReq struct {
	TemplateID  int64                       `json:"template_id" binding:"required"`
	HostIDs     []int64                     `json:"host_ids" binding:"required,min=1"`
	Params      map[string]string           `json:"params"`       // 任务级默认变量
	HostParams  map[int64]map[string]string `json:"host_params"`  // 主机级变量覆盖 host_id -> {k:v}
	Configs     map[string]string           `json:"configs"`      // 任务级自定义配置 config_key -> 内容（非空则覆盖默认配置文件）
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
	ids := DedupInt64(hostIDs)

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
		if _, err := RenderScript(tpl.Script, tpl.Variables, merged); err != nil {
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

// Tasks GET /api/deploy/tasks
func (h *deployHandler) Tasks(c *gin.Context) {
	page, pageSize := shared.ParsePage(c)
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
