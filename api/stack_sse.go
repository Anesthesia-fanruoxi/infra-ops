package api

import (
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
)

// SSESetup GET /api/sse/stacks/setup：套件运行列表常驻状态流。
func (h *stackHandler) SSESetup(c *gin.Context) {
	flusher, ok := sseSetup(c)
	if !ok {
		return
	}
	running, _ := h.repo.ListRunningRuns()
	snap := make([]gin.H, 0, len(running))
	for _, t := range running {
		snap = append(snap, gin.H{"run_id": t.ID, "stack_name": t.StackName, "mode": t.Mode, "status": t.Status,
			"total": t.Total, "success_cnt": t.SuccessCnt, "fail_cnt": t.FailCnt})
	}
	c.SSEvent("init", gin.H{"idle": len(snap) == 0, "running": snap})
	flusher.Flush()

	ch := make(chan stackProgress, 128)
	if h.bus != nil {
		subID := h.bus.Subscribe(eventbus.TopicStackProgress, func(ev eventbus.Event) {
			if p, ok := ev.Data.(stackProgress); ok {
				select {
				case ch <- p:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicStackProgress, subID)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case p := <-ch:
			if p.RunStatus != "running" {
				c.SSEvent("done", gin.H{"run_id": p.RunID, "status": p.RunStatus,
					"success_cnt": p.SuccessCnt, "fail_cnt": p.FailCnt, "total": p.Total})
			} else {
				c.SSEvent("track", gin.H{"run_id": p.RunID, "host_id": p.HostID, "phase": p.Phase, "status": p.Status,
					"prereq_status": p.PrereqStatus, "node_status": p.NodeStatus, "bootstrap_status": p.BootstrapStatus})
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

// SSELog GET /api/sse/stacks/log?run_id=N
func (h *stackHandler) SSELog(c *gin.Context) {
	runID, err := strconv.ParseInt(c.Query("run_id"), 10, 64)
	if err != nil || runID <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "run_id 无效")
		return
	}
	flusher, ok := sseSetup(c)
	if !ok {
		return
	}

	runStatus := "running"
	if t, _ := h.repo.GetRun(runID); t != nil {
		runStatus = t.Status
	}
	logs, _ := h.repo.RunLogs(runID)
	ls := make([]gin.H, 0, len(logs))
	for _, l := range logs {
		ls = append(ls, gin.H{"id": l.ID, "ts": l.CreatedAt, "ip": l.HostIP, "phase": l.Phase, "text": l.Text})
	}
	c.SSEvent("init", gin.H{"run_id": runID, "run_status": runStatus, "logs": ls})
	flusher.Flush()

	if runStatus != "running" {
		c.SSEvent("done", gin.H{"run_status": runStatus})
		flusher.Flush()
		return
	}

	ch := make(chan stackLogEvent, 256)
	doneCh := make(chan string, 4)
	if h.bus != nil {
		subLog := h.bus.Subscribe(eventbus.TopicStackLogs, func(ev eventbus.Event) {
			if l, ok := ev.Data.(stackLogEvent); ok && l.RunID == runID {
				select {
				case ch <- l:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicStackLogs, subLog)
		subProg := h.bus.Subscribe(eventbus.TopicStackProgress, func(ev eventbus.Event) {
			if p, ok := ev.Data.(stackProgress); ok && p.RunID == runID && p.RunStatus != "running" {
				select {
				case doneCh <- p.RunStatus:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicStackProgress, subProg)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case l := <-ch:
			c.SSEvent("log", gin.H{"id": l.ID, "ts": l.Ts, "ip": l.HostIP, "phase": l.Phase, "text": l.Text})
		case ts := <-doneCh:
			c.SSEvent("done", gin.H{"run_status": ts})
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
