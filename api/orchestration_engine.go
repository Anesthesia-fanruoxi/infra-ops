// 编排运行引擎：步骤严格串行、步骤内主机并行，事件发布与日志落库。
package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"infra-ops/common/eventbus"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store"
)

// orchProgress 主机状态事件（TopicOrchestrationProgress）。
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

// orchStepEvent 步骤生命周期事件（TopicOrchestrationSteps）。
type orchStepEvent struct {
	RunID     int64  `json:"run_id"`
	Seq       int    `json:"seq"`
	Name      string `json:"name,omitempty"`
	State     string `json:"state"`                // started / finished / run_done
	Aggregate string `json:"aggregate,omitempty"`  // finished 时: success/partial/failed/skipped
	RunStatus string `json:"run_status,omitempty"` // run_done 时: 运行终态
}

// orchLogEvent 日志行事件（TopicOrchestrationLogs，先落库再发布）。
type orchLogEvent struct {
	RunID  int64  `json:"run_id"`
	Seq    int    `json:"seq"`
	HostID int64  `json:"host_id"`
	HostIP string `json:"host_ip"`
	Text   string `json:"text"`
	ID     int64  `json:"id"`
	Ts     string `json:"ts,omitempty"`
}

// executeRun 引擎：步骤严格串行；步骤内主机并行（自适应并发）。
// 变量合并：模板默认 < 步骤默认 < 主机覆盖。某主机某步失败且未开「失败继续」时，
// 该主机不再参与后续步骤（明细置 skipped），其余主机不受影响 —— 长任务可安全跑完。
// runID 由 Run 同步创建后传入，本函数只负责执行与进度推送。
func (h *orchHandler) executeRun(orchID, runID int64) {
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
	// 仅用于确定并发度，运行明细（pending 步骤）已由 createRun 写入 DB。
	distinctHosts := map[int64]bool{}
	for _, def := range stepsDef {
		var hostIDs []int64
		_ = json.Unmarshal([]byte(def.HostScope), &hostIDs)
		for _, hid := range dedupInt64(hostIDs) {
			distinctHosts[hid] = true
		}
	}

	dead := map[int64]bool{} // 失败且未开「失败继续」的主机：不再参与后续步骤

	publish := func(hostID int64, seq int, status, output, errMsg string, attempt int) {
		if h.bus == nil {
			return
		}
		h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID,
			HostID: hostID, Seq: seq, Status: status, Output: output, Error: errMsg, Attempt: attempt, RunStatus: "running"})
	}
	publishStep := func(ev orchStepEvent) {
		if h.bus != nil {
			h.bus.Publish(eventbus.TopicOrchestrationSteps, ev)
		}
	}
	// publishLogs 将落库后的日志行发布到日志 topic。
	publishLogs := func(rows []model.OrchestrationRunLog) {
		if h.bus == nil {
			return
		}
		for _, lg := range rows {
			h.bus.Publish(eventbus.TopicOrchestrationLogs, orchLogEvent{RunID: runID, Seq: lg.Seq,
				HostID: lg.HostID, HostIP: lg.HostIP, Text: lg.Text, ID: lg.ID, Ts: lg.CreatedAt})
		}
	}
	// appendLog 引擎状态行/输出行：先落库再发布（保证重连 init 快照不丢行）。
	appendLog := func(hostID int64, seq int, ip, text string) {
		persisted, err := h.logRepo.AppendRunLogs(runID, []model.OrchestrationRunLog{{Seq: seq, HostID: hostID, HostIP: ip, Text: text}})
		if err != nil {
			fmt.Printf("orchestration: 写日志失败 run=%d seq=%d: %v\n", runID, seq, err)
			return
		}
		publishLogs(persisted)
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
			appendLog(cell.HostID, cell.Seq, cell.HostIP, fmt.Sprintf("开始执行（第 %d 次）", a))

			tpl, terr := h.tplRepo.GetTemplate(def.TemplateID)
			if terr != nil || tpl == nil {
				lastErr = "模板不存在或已删除"
				break
			}
			// 变量合并：模板默认 < 主机覆盖
			stepParams := map[string]string{}
			_ = json.Unmarshal([]byte(def.ParamsJSON), &stepParams)
			if stepParams == nil {
				stepParams = map[string]string{}
			}
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
				if chunk == "" {
					return
				}
				lines := splitLogLines(chunk)
				if len(lines) == 0 {
					return
				}
				rows := make([]model.OrchestrationRunLog, 0, len(lines))
				for _, ln := range lines {
					rows = append(rows, model.OrchestrationRunLog{Seq: cell.Seq, HostID: cell.HostID, HostIP: cell.HostIP, Text: ln})
				}
				persisted, err := h.logRepo.AppendRunLogs(runID, rows)
				if err != nil {
					fmt.Printf("orchestration: 写日志失败 run=%d seq=%d: %v\n", runID, cell.Seq, err)
					return
				}
				publishLogs(persisted)
			}
			out, execErr := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, cell.HostID, rendered, onLog)
			lastOut = out
			if execErr != nil {
				lastErr = execErr.Error()
				if a < attempts {
					appendLog(cell.HostID, cell.Seq, cell.HostIP, fmt.Sprintf("第 %d 次执行失败，%d 秒后重试", a, interval))
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
			appendLog(cell.HostID, cell.Seq, cell.HostIP, "执行成功")
			tname := cell.TemplateName
			if t, _ := h.tplRepo.GetTemplate(def.TemplateID); t != nil {
				tname = t.Name
			}
			if err := h.tplRepo.MarkHostInstalled(cell.HostID, def.TemplateID, tname, runID); err != nil {
				fmt.Printf("orchestration: 标记安装失败 host=%d: %v\n", cell.HostID, err)
			}
		} else {
			appendLog(cell.HostID, cell.Seq, cell.HostIP, "执行失败："+lastErr)
		}
		publish(cell.HostID, cell.Seq, status, lastOut, lastErr, attempts)

		if status == "failed" && !def.ContinueOnError {
			dead[cell.HostID] = true
			_ = h.repo.SkipRemaining(runID, cell.HostID, cell.Seq)
			publish(cell.HostID, 0, "skipped-refresh", "", "", 0)
			// 该主机后续 pending 步骤逐行发布 host 事件（status=skipped），详情流 L2 实时置灰
			for _, def2 := range stepsDef {
				if def2.Seq > cell.Seq {
					publish(cell.HostID, def2.Seq, "skipped", "", "", 0)
				}
			}
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
		publishStep(orchStepEvent{RunID: runID, Seq: def.Seq, Name: def.TemplateName, State: "started"})
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
		publishStep(orchStepEvent{RunID: runID, Seq: def.Seq, State: "finished",
			Aggregate: aggregateSeqStatus(allRows, def.Seq)})
	}

	finalStatus, _ := h.repo.FinishRun(runID)
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicOrchestrationProgress, orchProgress{RunID: runID, Status: "finished", RunStatus: finalStatus})
		h.bus.Publish(eventbus.TopicOrchestrationSteps, orchStepEvent{RunID: runID, State: "run_done", RunStatus: finalStatus})
	}
}

// aggregateSeqStatus 纯函数：由内存中的明细行聚合步骤状态。
func aggregateSeqStatus(rows []store.RunStepRef, seq int) string {
	var running, pending, skipped, okN, failN int
	for _, r := range rows {
		if r.Seq != seq {
			continue
		}
		switch r.Status {
		case "running":
			running++
		case "pending":
			pending++
		case "skipped":
			skipped++
		case "success":
			okN++
		case "failed":
			failN++
		}
	}
	switch {
	case running > 0:
		return "running"
	case pending > 0:
		return "pending"
	case skipped > 0 && okN == 0 && failN == 0:
		return "skipped"
	case failN == 0:
		return "success"
	case okN == 0:
		return "failed"
	default:
		return "partial"
	}
}

// splitLogLines 将输出块按行拆分：trim \r，跳过空行。
func splitLogLines(chunk string) []string {
	out := []string{}
	for _, line := range strings.Split(chunk, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
