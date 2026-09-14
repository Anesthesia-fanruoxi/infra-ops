package deploy

import (
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"sync"

	"infra-ops/api/shared"
	"infra-ops/common/eventbus"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store/repo"
)

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
	publish := func(rec repo.HostRecord, status, output, errMsg string) {
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
			if err := shared.RegisterTemplateServices(h.tplRepo, rec.HostID, rec.HostIP, task.TemplateID, vars); err != nil {
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
		go func(rec repo.HostRecord, seq int) {
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
				lines := shared.SplitLogLines(chunk)
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
			rendered, rerr := RenderScript(tpl.Script, tpl.Variables, params)
			// 自定义配置覆盖：若用户提供了非空内容，渲染期替换默认写入块
			rendered = applyConfigOverrides(rendered, tpl, params)
			if rerr != nil {
				publish(rec, "failed", "", "脚本渲染失败: "+rerr.Error())
				return
			}
			rendered = shared.ApplyHostVars(rendered, seq, rec)

			// 前置依赖检查：不满足则该主机直接失败，不执行主脚本
			if hint := CheckRequires(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, rec.HostID, TemplateRequires(tpl)); hint != "" {
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

// execOnHost 解密凭据→SSH 拨号→执行渲染后脚本；onLog 在执行过程中接收增量输出。
func (h *deployHandler) execOnHost(hostID int64, script string, onLog func(string)) (string, error) {
	return ExecHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hostID, script, onLog)
}

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
