// 部署执行记录 SSE 端点：实时进度流、常驻状态流与日志流。
package deploy

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
)

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

// SSESetup GET /api/sse/deploy/setup：执行记录常驻状态流（不传 task_id）。
// 连接时查询是否存在运行中任务：有则带快照并按主机粒度进度事件转发（运行→终态）；
// 无则只发 idle 并保持连接；常驻监听下，之后任意新启动的任务也会被实时转发。
func (h *deployHandler) SSESetup(c *gin.Context) {
	flusher, ok := shared.SSESetup(c)
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
	flusher, ok := shared.SSESetup(c)
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
