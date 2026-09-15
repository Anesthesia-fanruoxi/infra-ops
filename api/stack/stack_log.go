package stack

import (
	"log"

	"infra-ops/common/eventbus"
	"infra-ops/model"
)

// stackProgress SSE 进度事件载荷。
type stackProgress struct {
	RunID           int64  `json:"run_id"`
	HostID          int64  `json:"host_id,omitempty"`
	Phase           string `json:"phase,omitempty"`
	Status          string `json:"status"`
	PrereqStatus    string `json:"prereq_status,omitempty"`
	NodeStatus      string `json:"node_status,omitempty"`
	BootstrapStatus string `json:"bootstrap_status,omitempty"`
	// StepKey/StepStatus：流水线步骤状态迁移（与主机进度共用 TopicStackProgress，二者互斥出现）
	StepKey    string `json:"step_key,omitempty"`
	StepStatus string `json:"step_status,omitempty"`
	SuccessCnt int    `json:"success_cnt"`
	FailCnt    int    `json:"fail_cnt"`
	Total      int    `json:"total"`
	RunStatus  string `json:"run_status"`
}

// stackLogEvent SSE 日志事件载荷。
type stackLogEvent struct {
	RunID  int64  `json:"run_id"`
	Phase  string `json:"phase"`
	HostID int64  `json:"host_id"`
	HostIP string `json:"host_ip"`
	Text   string `json:"text"`
	ID     int64  `json:"id"`
	Ts     string `json:"ts,omitempty"`
}

// appendLog 持久化一条运行日志并广播。
func (h *stackHandler) appendLog(runID int64, phase string, hostID int64, hostIP, text string) {
	persisted, err := h.repo.AppendLogs(runID, []model.StackRunLog{{Phase: phase, HostID: hostID, HostIP: hostIP, Text: text}})
	if err != nil {
		log.Printf("stack: 写日志失败 run=%d: %v", runID, err)
		return
	}
	h.publishLogs(persisted)
}

// publishLogs 把一批日志逐条推送到 SSE 总线。
func (h *stackHandler) publishLogs(rows []model.StackRunLog) {
	if h.bus == nil {
		return
	}
	for _, l := range rows {
		h.bus.Publish(eventbus.TopicStackLogs, stackLogEvent{
			RunID: l.RunID, Phase: l.Phase, HostID: l.HostID, HostIP: l.HostIP,
			Text: l.Text, ID: l.ID, Ts: l.CreatedAt,
		})
	}
}

// publishHost 推送主机进度事件。
func (h *stackHandler) publishHost(runID int64, host *model.StackRunHost, phase string) {
	if h.bus == nil {
		return
	}
	h.bus.Publish(eventbus.TopicStackProgress, stackProgress{
		RunID: runID, HostID: host.HostID, Phase: phase, Status: host.Status,
		PrereqStatus: host.PrereqStatus, NodeStatus: host.NodeStatus, BootstrapStatus: host.BootstrapStatus,
		RunStatus: "running",
	})
}

// publishStep 推送流水线步骤状态迁移事件。
func (h *stackHandler) publishStep(runID int64, key, status string) {
	if h.bus == nil {
		return
	}
	h.bus.Publish(eventbus.TopicStackProgress, stackProgress{
		RunID: runID, StepKey: key, StepStatus: status, Status: status, RunStatus: "running",
	})
}

// stepRunning / stepDone：引擎推进步骤状态并广播（状态落库失败不阻断执行，日志已有记录）。
func (h *stackHandler) stepRunning(runID int64, key string) {
	if err := h.repo.SetStepStatus(runID, key, "running", ""); err != nil {
		log.Printf("stack: 步骤置 running 失败 run=%d step=%s: %v", runID, key, err)
		return
	}
	h.publishStep(runID, key, "running")
}

func (h *stackHandler) stepDone(runID int64, key string, ok bool) {
	status := "success"
	if !ok {
		status = "failed"
	}
	if err := h.repo.SetStepStatus(runID, key, status, ""); err != nil {
		log.Printf("stack: 步骤置终态失败 run=%d step=%s: %v", runID, key, err)
		return
	}
	h.publishStep(runID, key, status)
}
