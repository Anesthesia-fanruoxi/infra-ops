// 运行抽屉双 SSE 端点：步骤流（生命周期）+ 详情流（主机态+日志）。
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
)

// sseSetup 公共 SSE 响应头设置，返回 flusher。
func sseSetup(c *gin.Context) (http.Flusher, bool) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, "streaming not supported")
		return nil, false
	}
	return flusher, true
}

// SSESteps GET /api/sse/orchestration/steps?run_id=N：步骤生命周期流。
// 事件：init（全量步骤骨架）→ step（started/finished）→ done（run 结束，发送后关闭）。
func (h *orchHandler) SSESteps(c *gin.Context) {
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
	if run, _ := h.repo.GetRun(runID); run != nil {
		runStatus = run.Status
	}
	rows, _ := h.repo.RunSteps(runID)
	c.SSEvent("init", gin.H{"run_id": runID, "run_status": runStatus, "steps": h.stepsSnapshot(rows)})
	flusher.Flush()

	if runStatus != "running" { // 已结束运行：init → done 立即关闭（回溯语义）
		c.SSEvent("done", gin.H{"run_status": runStatus})
		flusher.Flush()
		return
	}

	ch := make(chan orchStepEvent, 128)
	var subID int
	if h.bus != nil {
		subID = h.bus.Subscribe(eventbus.TopicOrchestrationSteps, func(ev eventbus.Event) {
			if p, ok := ev.Data.(orchStepEvent); ok && p.RunID == runID {
				select {
				case ch <- p:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicOrchestrationSteps, subID)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case ev := <-ch:
			if ev.State == "run_done" {
				c.SSEvent("done", gin.H{"run_status": ev.RunStatus})
				flusher.Flush()
				return false
			}
			data, _ := json.Marshal(ev)
			c.SSEvent("step", string(data))
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		case <-c.Request.Context().Done():
			return false
		}
		flusher.Flush()
		return true
	})
}

// stepsSnapshot 由 run_steps 聚合每步骨架：seq/name/state/aggregate（按 seq 升序）。
func (h *orchHandler) stepsSnapshot(rows []store.RunStepRef) []gin.H {
	seqOrder := []int{}
	names := map[int]string{}
	seen := map[int]bool{}
	for _, r := range rows {
		if !seen[r.Seq] {
			seen[r.Seq] = true
			seqOrder = append(seqOrder, r.Seq)
		}
		if names[r.Seq] == "" && r.TemplateName != "" {
			names[r.Seq] = r.TemplateName
		}
	}
	out := make([]gin.H, 0, len(seqOrder))
	for _, seq := range seqOrder {
		agg := aggregateSeqStatus(rows, seq)
		state := "finished"
		if agg == "running" || agg == "pending" {
			state = "running"
		}
		out = append(out, gin.H{"seq": seq, "name": names[seq], "state": state, "aggregate": agg})
	}
	return out
}

// detailEvent 详情流内部统一事件（host/log/done）。
type detailEvent struct {
	kind      string // host / log / done
	seq       int
	hostID    int64
	status    string
	attempt   int
	logID     int64
	ts        string
	ip        string
	text      string
	runStatus string
}

// SSEDetail GET /api/sse/orchestration/detail?run_id=N&step=M：单步骤主机态+日志流。
// 事件：init（快照：该步骤主机态 + 已落库日志最近 2000 行）→ host/log 增量 → done（结束关闭）。
func (h *orchHandler) SSEDetail(c *gin.Context) {
	runID, err := strconv.ParseInt(c.Query("run_id"), 10, 64)
	step, serr := strconv.Atoi(c.Query("step"))
	if err != nil || runID <= 0 || serr != nil || step <= 0 {
		resp.Fail(c, resp.CodeBadRequest, "run_id/step 无效")
		return
	}
	flusher, ok := sseSetup(c)
	if !ok {
		return
	}

	runStatus := "running"
	if run, _ := h.repo.GetRun(runID); run != nil {
		runStatus = run.Status
	}
	rows, _ := h.repo.RunSteps(runID)
	logs, _ := h.logRepo.RunStepLogs(runID, step)
	c.SSEvent("init", gin.H{
		"step": step, "run_status": runStatus,
		"hosts": detailHosts(rows, step),
		"logs":  detailLogs(logs),
	})
	flusher.Flush()

	if runStatus != "running" { // 已结束：init → done 立即关闭（回溯语义）
		c.SSEvent("done", gin.H{"run_status": runStatus})
		flusher.Flush()
		return
	}

	ch := make(chan detailEvent, 256)
	if h.bus != nil {
		subP := h.bus.Subscribe(eventbus.TopicOrchestrationProgress, func(ev eventbus.Event) {
			p, ok := ev.Data.(orchProgress)
			if !ok || p.RunID != runID {
				return
			}
			if p.Status == "finished" {
				select {
				case ch <- detailEvent{kind: "done", runStatus: p.RunStatus}:
				default:
				}
				return
			}
			if p.Seq == step {
				select {
				case ch <- detailEvent{kind: "host", seq: p.Seq, hostID: p.HostID, status: p.Status, attempt: p.Attempt}:
				default:
				}
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicOrchestrationProgress, subP)

		subL := h.bus.Subscribe(eventbus.TopicOrchestrationLogs, func(ev eventbus.Event) {
			l, ok := ev.Data.(orchLogEvent)
			if !ok || l.RunID != runID || l.Seq != step {
				return
			}
			select {
			case ch <- detailEvent{kind: "log", seq: l.Seq, logID: l.ID, ts: l.Ts, ip: l.HostIP, text: l.Text}:
			default:
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicOrchestrationLogs, subL)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case de := <-ch:
			switch de.kind {
			case "done":
				c.SSEvent("done", gin.H{"run_status": de.runStatus})
				flusher.Flush()
				return false
			case "host":
				c.SSEvent("host", gin.H{"seq": de.seq, "host_id": de.hostID, "status": de.status, "attempt": de.attempt})
			case "log":
				c.SSEvent("log", gin.H{"id": de.logID, "seq": de.seq, "ts": de.ts, "ip": de.ip, "text": de.text})
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

// detailHosts 该步骤的主机态快照（每台主机取一行，含 status）。
func detailHosts(rows []store.RunStepRef, step int) []gin.H {
	out := []gin.H{}
	seen := map[int64]bool{}
	for _, r := range rows {
		if r.Seq != step || seen[r.HostID] {
			continue
		}
		seen[r.HostID] = true
		out = append(out, gin.H{"host_id": r.HostID, "host_ip": r.HostIP, "host_name": r.HostName, "status": r.Status})
	}
	return out
}

// detailLogs 该步骤日志快照（id/ts/ip/text）。
func detailLogs(logs []model.OrchestrationRunLog) []gin.H {
	out := make([]gin.H, 0, len(logs))
	for _, l := range logs {
		out = append(out, gin.H{"id": l.ID, "ts": l.CreatedAt, "ip": l.HostIP, "text": l.Text})
	}
	return out
}

// RunsDetail GET /api/orchestration/runs/:id：运行详情（抽屉打开时 GET 打底）。
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
