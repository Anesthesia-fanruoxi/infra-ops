package api

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"infra-ops/common/eventbus"
	"infra-ops/common/sysutil"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/repo"
)

type stackProgress struct {
	RunID           int64  `json:"run_id"`
	HostID          int64  `json:"host_id,omitempty"`
	Phase           string `json:"phase,omitempty"`
	Status          string `json:"status"`
	PrereqStatus    string `json:"prereq_status,omitempty"`
	NodeStatus      string `json:"node_status,omitempty"`
	BootstrapStatus string `json:"bootstrap_status,omitempty"`
	SuccessCnt      int    `json:"success_cnt"`
	FailCnt         int    `json:"fail_cnt"`
	Total           int    `json:"total"`
	RunStatus       string `json:"run_status"`
}

type stackLogEvent struct {
	RunID  int64  `json:"run_id"`
	Phase  string `json:"phase"`
	HostID int64  `json:"host_id"`
	HostIP string `json:"host_ip"`
	Text   string `json:"text"`
	ID     int64  `json:"id"`
	Ts     string `json:"ts,omitempty"`
}

func (h *stackHandler) execute(runID int64) {
	run, err := h.repo.GetRun(runID)
	if err != nil || run == nil {
		return
	}
	hosts, err := h.repo.RunHosts(runID)
	if err != nil || len(hosts) == 0 {
		_, _ = h.repo.FinishRun(runID)
		return
	}
	bp := store.FindBuiltinStack(run.StackKey)
	if bp == nil {
		h.failAll(runID, hosts, "套件蓝图不存在")
		return
	}
	mode := bp.ModeDef(run.Mode)
	if mode == nil {
		h.failAll(runID, hosts, "未知模式")
		return
	}
	op := strings.TrimSpace(run.Op)
	if op == "" {
		op = "create"
	}

	if op == "scale_in" || op == "uninstall" || op == "remove_component" {
		h.runPhaseRemove(runID, run, hosts)
		h.finish(runID)
		return
	}

	existing := []model.StackRunHost{}
	if run.InstanceID > 0 && (op == "scale_out" || op == "add_component") {
		if instHosts, err := h.repo.InstanceHosts(run.InstanceID, true); err == nil {
			existing = instanceHostsAsRunHosts(instHosts)
		}
	}
	extraAll := stackClusterExtraMerged(op, existing, hosts)

	if bp.RequiresDocker {
		ok := h.runPhasePrereq(runID, hosts)
		hosts, _ = h.repo.RunHosts(runID)
		if !ok {
			h.skipRemaining(hosts, true, true)
			h.finish(runID)
			return
		}
	} else {
		h.skipPhasePrereq(runID, hosts)
		hosts, _ = h.repo.RunHosts(runID)
	}

	ok := h.runPhaseAllHosts(runID, run.Mode, bp, hosts, extraAll)
	hosts, _ = h.repo.RunHosts(runID)
	if !ok {
		h.skipRemaining(hosts, false, true)
		h.finish(runID)
		return
	}

	if op == "scale_out" && bp.Key == "redis" && run.Mode == "cluster" {
		h.runPhaseScaleOut(runID, run.Mode, bp, hosts, existing, extraAll)
		h.finish(runID)
		return
	}
	if op == "create" && mode.HasBootstrap {
		h.runPhaseBootstrap(runID, run.Mode, bp, hosts)
		h.finish(runID)
		return
	}
	h.markHostsDone(hosts)
	h.finish(runID)
}

func (h *stackHandler) failAll(runID int64, hosts []model.StackRunHost, msg string) {
	for i := range hosts {
		hosts[i].Status = "failed"
		hosts[i].Error = msg
		hosts[i].PrereqStatus = "failed"
		hosts[i].NodeStatus = "skipped"
		hosts[i].BootstrapStatus = "skipped"
		_ = h.repo.UpdateHost(&hosts[i])
	}
	h.finish(runID)
}

func (h *stackHandler) skipRemaining(hosts []model.StackRunHost, skipNode, skipBoot bool) {
	for i := range hosts {
		if skipNode && (hosts[i].NodeStatus == "pending" || hosts[i].NodeStatus == "running") {
			hosts[i].NodeStatus = "skipped"
			if hosts[i].Status != "failed" {
				hosts[i].Status = "failed"
				if hosts[i].Error == "" {
					hosts[i].Error = "因其他主机失败，未执行后续阶段"
				}
			}
		}
		if skipBoot && (hosts[i].BootstrapStatus == "pending" || hosts[i].BootstrapStatus == "running") {
			hosts[i].BootstrapStatus = "skipped"
		}
		if hosts[i].Status != "failed" && hosts[i].Status != "success" {
			hosts[i].Status = stackHostOverall(hosts[i])
		}
		_ = h.repo.UpdateHost(&hosts[i])
	}
}

func (h *stackHandler) markHostsDone(hosts []model.StackRunHost) {
	for i := range hosts {
		hosts[i].Status = stackHostOverall(hosts[i])
		_ = h.repo.UpdateHost(&hosts[i])
	}
}

func stackHostOverall(h model.StackRunHost) string {
	if h.PrereqStatus == "failed" || h.NodeStatus == "failed" || h.BootstrapStatus == "failed" {
		return "failed"
	}
	for _, s := range []string{h.PrereqStatus, h.NodeStatus, h.BootstrapStatus} {
		if s == "pending" || s == "running" {
			return "running"
		}
	}
	if h.PrereqStatus == "skipped" && h.NodeStatus == "skipped" {
		return "failed"
	}
	return "success"
}

func (h *stackHandler) finish(runID int64) {
	hosts, _ := h.repo.RunHosts(runID)
	h.markHostsDone(hosts)
	status, _ := h.repo.FinishRun(runID)
	sc, fc, total := 0, 0, len(hosts)
	if run, _ := h.repo.GetRun(runID); run != nil {
		sc, fc, total = run.SuccessCnt, run.FailCnt, run.Total
		h.syncInstanceAfterRun(run, status, hosts)
	}
	if h.bus != nil {
		h.bus.Publish(eventbus.TopicStackProgress, stackProgress{
			RunID: runID, Status: "finished", RunStatus: status,
			SuccessCnt: sc, FailCnt: fc, Total: total,
		})
	}
}

func (h *stackHandler) skipPhasePrereq(runID int64, hosts []model.StackRunHost) {
	h.appendLog(runID, "prereq", 0, "", "该套件为主机安装，跳过 Docker 检查")
	for i := range hosts {
		host := &hosts[i]
		host.PrereqStatus = "skipped"
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "prereq")
	}
}

func (h *stackHandler) runPhasePrereq(runID int64, hosts []model.StackRunHost) bool {
	h.appendLog(runID, "prereq", 0, "", "开始检查 Docker（已安装则跳过，不改动现有配置）")
	dockerTpl, _ := h.tplRepo.GetTemplateByName(store.DockerTemplateName)
	var failed int32
	h.forEachHost(hosts, func(host *model.StackRunHost) {
		host.Status = "running"
		host.PrereqStatus = "running"
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "prereq")

		present, probeErr := h.probeDocker(host.HostID)
		if probeErr != nil {
			host.PrereqStatus = "failed"
			host.Status = "failed"
			host.Error = "探测 Docker 失败: " + probeErr.Error()
			h.appendLog(runID, "prereq", host.HostID, host.HostIP, "探测失败: "+probeErr.Error())
			atomic.StoreInt32(&failed, 1)
			_ = h.repo.UpdateHost(host)
			h.publishHost(runID, host, "prereq")
			return
		}
		if present {
			host.PrereqStatus = "skipped"
			h.appendLog(runID, "prereq", host.HostID, host.HostIP, "已安装 Docker，跳过且不改动")
			_ = h.repo.UpdateHost(host)
			h.publishHost(runID, host, "prereq")
			return
		}
		if dockerTpl == nil {
			host.PrereqStatus = "failed"
			host.Status = "failed"
			host.Error = "找不到内置模板「安装 Docker」"
			atomic.StoreInt32(&failed, 1)
			h.appendLog(runID, "prereq", host.HostID, host.HostIP, host.Error)
			_ = h.repo.UpdateHost(host)
			h.publishHost(runID, host, "prereq")
			return
		}
		h.appendLog(runID, "prereq", host.HostID, host.HostIP, "未检测到 Docker，开始安装")
		out, execErr := h.execScript(host, dockerTpl.Script, "prereq")
		host.Output = out
		if execErr != nil {
			host.PrereqStatus = "failed"
			host.Status = "failed"
			host.Error = execErr.Error()
			atomic.StoreInt32(&failed, 1)
			h.appendLog(runID, "prereq", host.HostID, host.HostIP, "安装 Docker 失败: "+execErr.Error())
		} else {
			host.PrereqStatus = "success"
			h.appendLog(runID, "prereq", host.HostID, host.HostIP, "Docker 安装完成")
		}
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "prereq")
	})
	return atomic.LoadInt32(&failed) == 0
}

func (h *stackHandler) runPhaseAllHosts(runID int64, mode string, bp *store.BuiltinStack, hosts []model.StackRunHost, extraAll map[int64]map[string]string) bool {
	script, err := bp.LoadPhase(mode, "node")
	if err != nil {
		h.appendLog(runID, "node", 0, "", "加载节点脚本失败: "+err.Error())
		return false
	}
	varsJSON := stackVarsJSON(bp.Blueprint(), mode)
	if extraAll == nil {
		extraAll = stackClusterExtra(hosts)
	}
	h.appendLog(runID, "node", 0, "", "开始部署 "+bp.Name+" 节点")
	var failed int32
	h.forEachHost(hosts, func(host *model.StackRunHost) {
		if host.PrereqStatus != "success" && host.PrereqStatus != "skipped" {
			host.NodeStatus = "skipped"
			host.Status = "failed"
			_ = h.repo.UpdateHost(host)
			h.publishHost(runID, host, "node")
			atomic.StoreInt32(&failed, 1)
			return
		}
		host.NodeStatus = "running"
		host.Status = "running"
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "node")

		params := map[string]string{}
		_ = json.Unmarshal([]byte(host.ParamsJSON), &params)
		rendered, rerr := renderScript(script, varsJSON, params)
		if rerr != nil {
			host.NodeStatus = "failed"
			host.Status = "failed"
			host.Error = "脚本渲染失败: " + rerr.Error()
			atomic.StoreInt32(&failed, 1)
			h.appendLog(runID, "node", host.HostID, host.HostIP, host.Error)
			_ = h.repo.UpdateHost(host)
			h.publishHost(runID, host, "node")
			return
		}
		extra := extraAll[host.HostID]
		rendered = applyStackVars(rendered, host.Seq, *host, extra)
		h.appendLog(runID, "node", host.HostID, host.HostIP, "开始执行节点脚本")
		out, execErr := h.execScript(host, rendered, "node")
		host.Output = out
		if execErr != nil {
			host.NodeStatus = "failed"
			host.Status = "failed"
			host.Error = execErr.Error()
			atomic.StoreInt32(&failed, 1)
			h.appendLog(runID, "node", host.HostID, host.HostIP, "节点部署失败: "+execErr.Error())
		} else {
			host.NodeStatus = "success"
			h.appendLog(runID, "node", host.HostID, host.HostIP, "节点已就绪")
			if bp.Key == "redis" {
				h.registerRedisService(host, mode, params)
			} else {
				h.registerStackService(bp, host, mode, params)
			}
		}
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "node")
	})
	return atomic.LoadInt32(&failed) == 0
}

func (h *stackHandler) runPhaseBootstrap(runID int64, mode string, bp *store.BuiltinStack, hosts []model.StackRunHost) {
	script, err := bp.LoadPhase(mode, "bootstrap")
	if err != nil {
		h.appendLog(runID, "bootstrap", 0, "", "加载初始化脚本失败: "+err.Error())
		h.skipRemaining(hosts, false, true)
		return
	}
	var leader *model.StackRunHost
	for i := range hosts {
		if hosts[i].BootstrapStatus == "pending" || hosts[i].Seq == 1 {
			leader = &hosts[i]
			break
		}
	}
	if leader == nil {
		return
	}
	leader.BootstrapStatus = "running"
	leader.Status = "running"
	_ = h.repo.UpdateHost(leader)
	h.publishHost(runID, leader, "bootstrap")

	params := map[string]string{}
	_ = json.Unmarshal([]byte(leader.ParamsJSON), &params)
	// bootstrap 脚本变量按套件蓝图当前模式声明，避免硬编码；{{__xxx}} 集群变量由 applyStackVars 注入
	varsJSON := stackVarsJSON(bp.Blueprint(), mode)
	if bp.Key == "redis" && strings.TrimSpace(params["replicas"]) == "" {
		params["replicas"] = "0"
	}
	rendered, rerr := renderScript(script, varsJSON, params)
	if rerr != nil {
		leader.BootstrapStatus = "failed"
		leader.Status = "failed"
		leader.Error = "脚本渲染失败: " + rerr.Error()
		h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, leader.Error)
		_ = h.repo.UpdateHost(leader)
		h.publishHost(runID, leader, "bootstrap")
		return
	}
	extra := stackClusterExtra(hosts)[leader.HostID]
	rendered = applyStackVars(rendered, leader.Seq, *leader, extra)
	h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, "开始初始化集群")
	out, execErr := h.execScript(leader, rendered, "bootstrap")
	leader.Output = out
	if execErr != nil {
		leader.BootstrapStatus = "failed"
		leader.Status = "failed"
		leader.Error = execErr.Error()
		h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, "集群初始化失败: "+execErr.Error())
	} else {
		leader.BootstrapStatus = "success"
		h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, "集群初始化完成")
	}
	_ = h.repo.UpdateHost(leader)
	h.publishHost(runID, leader, "bootstrap")
}

func (h *stackHandler) runPhaseRemove(runID int64, run *model.StackRun, hosts []model.StackRunHost) {
	op := "scale_in"
	removeComps := []string{}
	if run != nil {
		op = strings.TrimSpace(run.Op)
		if op == "" {
			op = "scale_in"
		}
		p := map[string]string{}
		_ = json.Unmarshal([]byte(run.ParamsJSON), &p)
		removeComps = parseComponentsCSV(p["remove_components"])
	}
	lead := map[string]string{
		"scale_in":         "开始缩容：停止选中节点上的套件服务",
		"uninstall":        "开始卸载集群：停止全部节点上的套件服务",
		"remove_component": "开始卸载组件：" + strings.Join(removeComps, ","),
	}[op]
	if lead == "" {
		lead = "开始停止套件服务"
	}
	h.appendLog(runID, "node", 0, "", lead)
	h.forEachHost(hosts, func(host *model.StackRunHost) {
		host.Status = "running"
		host.NodeStatus = "running"
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "node")
		params := map[string]string{}
		_ = json.Unmarshal([]byte(host.ParamsJSON), &params)
		home := strings.TrimSpace(params["home_dir"])
		if home == "" {
			host.NodeStatus = "failed"
			host.Status = "failed"
			host.Error = "缺少服务主目录，无法停服"
			h.appendLog(runID, "node", host.HostID, host.HostIP, host.Error)
			_ = h.repo.UpdateHost(host)
			h.publishHost(runID, host, "node")
			return
		}
		script := stackComposeDownScript(home, removeComps)
		out, execErr := h.execScript(host, script, "node")
		host.Output = out
		if execErr != nil {
			host.NodeStatus = "failed"
			host.Status = "failed"
			host.Error = execErr.Error()
			h.appendLog(runID, "node", host.HostID, host.HostIP, "停服失败: "+execErr.Error())
		} else {
			host.NodeStatus = "success"
			host.Status = "success"
			h.appendLog(runID, "node", host.HostID, host.HostIP, "本机套件服务已停止")
		}
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "node")
	})
}

func stackComposeDownScript(home string, comps []string) string {
	dirs := []string{}
	if len(comps) == 0 {
		dirs = []string{"", "hadoop", "zookeeper", "yarn", "spark", "flink", "hive", "hbase", "trino"}
	} else {
		for _, c := range comps {
			switch c {
			case "hdfs":
				dirs = append(dirs, "hadoop")
			default:
				dirs = append(dirs, c)
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HOME_DIR=%q\n", home)
	b.WriteString("down_one() { if [ -f \"$1\" ]; then docker compose -f \"$1\" down || true; echo \"已停止 $1\"; fi; }\n")
	for _, d := range dirs {
		if d == "" {
			b.WriteString("down_one \"${HOME_DIR}/compose.yml\"\n")
			continue
		}
		fmt.Fprintf(&b, "down_one \"${HOME_DIR}/%s/compose.yml\"\n", d)
	}
	return b.String()
}

func (h *stackHandler) runPhaseScaleOut(runID int64, mode string, bp *store.BuiltinStack, newHosts, existing []model.StackRunHost, extraAll map[int64]map[string]string) {
	script, err := bp.LoadPhase(mode, "scale_out")
	if err != nil {
		h.appendLog(runID, "bootstrap", 0, "", "无扩容初始化脚本，节点已启动")
		h.markHostsDone(newHosts)
		return
	}
	var leader *model.StackRunHost
	for i := range newHosts {
		if newHosts[i].BootstrapStatus == "pending" {
			leader = &newHosts[i]
			break
		}
	}
	if leader == nil && len(newHosts) > 0 {
		leader = &newHosts[0]
	}
	if leader == nil {
		return
	}
	execTarget := leader
	for i := range existing {
		if existing[i].Role == "master" {
			execTarget = &existing[i]
			break
		}
		if i == 0 {
			execTarget = &existing[0]
		}
	}
	if len(existing) > 0 && execTarget == leader {
		execTarget = &existing[0]
	}

	leader.BootstrapStatus = "running"
	leader.Status = "running"
	_ = h.repo.UpdateHost(leader)
	h.publishHost(runID, leader, "bootstrap")

	params := map[string]string{}
	_ = json.Unmarshal([]byte(execTarget.ParamsJSON), &params)
	if strings.TrimSpace(params["replicas"]) == "" {
		params["replicas"] = "0"
	}
	varsJSON := stackVarsJSON(bp.Blueprint(), mode)
	rendered, rerr := renderScript(script, varsJSON, params)
	if rerr != nil {
		leader.BootstrapStatus = "failed"
		leader.Status = "failed"
		leader.Error = "脚本渲染失败: " + rerr.Error()
		h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, leader.Error)
		_ = h.repo.UpdateHost(leader)
		h.publishHost(runID, leader, "bootstrap")
		return
	}
	extra := extraAll[execTarget.HostID]
	if extra == nil {
		extra = extraAll[leader.HostID]
	}
	rendered = applyStackVars(rendered, execTarget.Seq, *execTarget, extra)
	target := *execTarget
	target.RunID = runID
	h.appendLog(runID, "bootstrap", target.HostID, target.HostIP, "开始将新节点加入现有集群")
	out, execErr := h.execScript(&target, rendered, "bootstrap")
	leader.Output = out
	if execErr != nil {
		leader.BootstrapStatus = "failed"
		leader.Status = "failed"
		leader.Error = execErr.Error()
		h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, "加节点失败: "+execErr.Error())
	} else {
		leader.BootstrapStatus = "success"
		h.appendLog(runID, "bootstrap", leader.HostID, leader.HostIP, "新节点已加入集群")
	}
	_ = h.repo.UpdateHost(leader)
	h.publishHost(runID, leader, "bootstrap")
}

func (h *stackHandler) syncInstanceAfterRun(run *model.StackRun, status string, hosts []model.StackRunHost) {
	if run == nil || run.InstanceID == 0 {
		return
	}
	inst, err := h.repo.GetInstance(run.InstanceID)
	if err != nil || inst == nil {
		return
	}
	op := strings.TrimSpace(run.Op)
	if op == "" {
		op = "create"
	}
	for i := range hosts {
		host := hosts[i]
		if host.Status != "success" {
			continue
		}
		switch op {
		case "scale_in":
			_ = h.repo.MarkInstanceHostRemoved(run.InstanceID, host.HostID)
		case "uninstall":
			_ = h.repo.MarkInstanceHostRemoved(run.InstanceID, host.HostID)
		case "remove_component":
			params := map[string]string{}
			_ = json.Unmarshal([]byte(run.ParamsJSON), &params)
			remain := parseComponentsCSV(params["components"])
			oldComps := remain
			if existing, err := h.repo.InstanceHosts(run.InstanceID, false); err == nil {
				for _, eh := range existing {
					if eh.HostID == host.HostID {
						oldComps = subtractComponents(parseJSONStringList(eh.ComponentsJSON), parseComponentsCSV(params["remove_components"]))
						if len(oldComps) == 0 {
							oldComps = remain
						}
						break
					}
				}
			}
			_ = h.repo.UpsertInstanceHost(&model.StackInstanceHost{
				InstanceID: run.InstanceID, HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
				Role: host.Role, Seq: host.Seq, ParamsJSON: host.ParamsJSON,
				ComponentsJSON: encodeJSONStringList(oldComps), Status: "active",
			})
		default:
			params := map[string]string{}
			_ = json.Unmarshal([]byte(run.ParamsJSON), &params)
			comps := parseJSONStringList(inst.ComponentsJSON)
			if run.StackKey == "bigdata" {
				comps = parseComponentsCSV(params["components"])
			} else if op == "add_component" {
				comps = mergeStringList(comps, run.Mode)
			} else if op == "create" && !containsString(comps, run.Mode) {
				comps = mergeStringList(comps, run.Mode)
			}
			hostComps := comps
			if existing, err := h.repo.InstanceHosts(run.InstanceID, false); err == nil {
				for _, eh := range existing {
					if eh.HostID == host.HostID {
						hostComps = mergeComponentList(parseJSONStringList(eh.ComponentsJSON), comps)
						break
					}
				}
			}
			_ = h.repo.UpsertInstanceHost(&model.StackInstanceHost{
				InstanceID: run.InstanceID, HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
				Role: host.Role, Seq: host.Seq, ParamsJSON: host.ParamsJSON,
				ComponentsJSON: encodeJSONStringList(hostComps), Status: "active",
			})
			if op == "add_component" || op == "create" {
				_ = h.repo.UpdateInstance(run.InstanceID, "", "", "", encodeJSONStringList(comps))
			}
		}
	}
	instStatus := "ready"
	switch {
	case op == "uninstall" && status == "success":
		instStatus = "uninstalled"
	case status == "success":
		instStatus = "ready"
	case status == "partial":
		instStatus = "partial"
	case op == "create":
		instStatus = "failed"
	default:
		instStatus = "partial"
	}
	paramsJSON, compsJSON := "", ""
	if op == "remove_component" {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(run.ParamsJSON), &p)
		compsJSON = encodeJSONStringList(parseComponentsCSV(p["components"]))
		if status == "success" {
			paramsJSON = run.ParamsJSON
		}
	}
	_ = h.repo.UpdateInstance(run.InstanceID, "", instStatus, paramsJSON, compsJSON)
}

func (h *stackHandler) probeDocker(hostID int64) (bool, error) {
	out, err := execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, hostID,
		`if command -v docker >/dev/null 2>&1; then echo INFRAOPS_DOCKER=yes; else echo INFRAOPS_DOCKER=no; fi`, nil)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "INFRAOPS_DOCKER=yes"), nil
}

func (h *stackHandler) execScript(host *model.StackRunHost, script, phase string) (string, error) {
	onLog := func(chunk string) {
		if chunk == "" {
			return
		}
		lines := splitLogLines(chunk)
		if len(lines) == 0 {
			return
		}
		rows := make([]model.StackRunLog, 0, len(lines))
		for _, ln := range lines {
			rows = append(rows, model.StackRunLog{Phase: phase, HostID: host.HostID, HostIP: host.HostIP, Text: ln})
		}
		persisted, err := h.repo.AppendLogs(host.RunID, rows)
		if err != nil {
			log.Printf("stack: 写日志失败 run=%d host=%d: %v", host.RunID, host.HostID, err)
			return
		}
		h.publishLogs(persisted)
	}
	return execHostWith(h.hostRepo, h.credRepo, h.cryptoS, h.sshC, host.HostID, script, onLog)
}

func (h *stackHandler) forEachHost(hosts []model.StackRunHost, fn func(*model.StackRunHost)) {
	conc := h.conc
	if conc <= 0 {
		conc = sysutil.AdaptiveConcurrency(len(hosts))
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range hosts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			host := hosts[idx]
			mu.Unlock()
			fn(&host)
			mu.Lock()
			hosts[idx] = host
			mu.Unlock()
		}(i)
	}
	wg.Wait()
}

func (h *stackHandler) appendLog(runID int64, phase string, hostID int64, hostIP, text string) {
	persisted, err := h.repo.AppendLogs(runID, []model.StackRunLog{{Phase: phase, HostID: hostID, HostIP: hostIP, Text: text}})
	if err != nil {
		log.Printf("stack: 写日志失败 run=%d: %v", runID, err)
		return
	}
	h.publishLogs(persisted)
}

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

func (h *stackHandler) registerRedisService(host *model.StackRunHost, mode string, params map[string]string) {
	port := params["port"]
	if port == "" {
		port = "6379"
	}
	name := "Redis Cluster"
	if mode == "replication" || mode == "sentinel" {
		if host.Role == "master" {
			name = "Redis 主"
		} else {
			name = "Redis 从"
		}
	}
	_ = h.tplRepo.UpsertHostService(&model.HostService{
		HostID: host.HostID, HostIP: host.HostIP, ServiceName: name,
		URL: fmt.Sprintf("redis://%s:%s", host.HostIP, port), Web: false, TemplateID: 0,
	})
	if mode == "sentinel" {
		sPort := params["sentinel_port"]
		if sPort == "" {
			sPort = "26379"
		}
		_ = h.tplRepo.UpsertHostService(&model.HostService{
			HostID: host.HostID, HostIP: host.HostIP, ServiceName: "Redis Sentinel",
			URL: fmt.Sprintf("redis-sentinel://%s:%s", host.HostIP, sPort), Web: false, TemplateID: 0,
		})
	}
	_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:Redis/"+mode, 0)
}

// registerStackService 通用套件服务注册：按套件 Key + 角色登记服务入口与安装台账。
func (h *stackHandler) registerStackService(bp *store.BuiltinStack, host *model.StackRunHost, mode string, params map[string]string) {
	type svcEntry struct {
		name string
		url  string
		web  bool
	}
	pick := func(key, def string) string {
		if v := strings.TrimSpace(params[key]); v != "" {
			return v
		}
		return def
	}
	ip := host.HostIP
	// 大数据底座：按勾选组件 + 角色登记多个服务入口
	if bp.Key == "bigdata" {
		h.registerBigdataServices(host, params)
		return
	}
	registry := map[string]map[string]svcEntry{
		"kafka": {
			"node": {name: "Kafka Broker", url: "kafka://" + ip + ":" + pick("port", "9092"), web: false},
		},
		"elasticsearch/cluster": {
			"master": {name: "Elasticsearch", url: "http://" + ip + ":" + pick("port", "9200"), web: true},
			"worker": {name: "Elasticsearch", url: "http://" + ip + ":" + pick("port", "9200"), web: true},
		},
		"rabbitmq/cluster": {
			"master": {name: "RabbitMQ", url: "http://" + ip + ":" + pick("mgmt_port", "15672"), web: true},
			"worker": {name: "RabbitMQ", url: "http://" + ip + ":" + pick("mgmt_port", "15672"), web: true},
		},
		"rocketmq/namesrv": {
			"node": {name: "RocketMQ NameServer", url: "rocketmq://" + ip + ":" + pick("port", "9876"), web: false},
		},
		"rocketmq/broker": {
			"master": {name: "RocketMQ Broker 主", url: "rocketmq://" + ip + ":" + pick("broker_port", "10911"), web: false},
			"worker": {name: "RocketMQ Broker 从", url: "rocketmq://" + ip + ":" + pick("broker_port", "10911"), web: false},
		},
	}
	byRole, ok := registry[bp.Key+"/"+mode]
	if !ok {
		byRole = registry[bp.Key]
	}
	if byRole == nil {
		byRole = map[string]svcEntry{
			"master": {name: bp.Name + " 主节点", url: "http://" + ip, web: false},
			"worker": {name: bp.Name + " 工作节点", url: "http://" + ip, web: false},
		}
	}
	svc, ok := byRole[host.Role]
	if !ok {
		svc = svcEntry{name: bp.Name + " 节点", url: "http://" + ip, web: false}
	}
	_ = h.tplRepo.UpsertHostService(&model.HostService{
		HostID: host.HostID, HostIP: ip, ServiceName: svc.name,
		URL: svc.url, Web: svc.web, TemplateID: 0,
	})
	if bp.Key == "kafka" && mode == "zk" {
		_ = h.tplRepo.UpsertHostService(&model.HostService{
			HostID: host.HostID, HostIP: ip, ServiceName: "ZooKeeper",
			URL: "zookeeper://" + ip + ":" + pick("zk_port", "2181"), Web: false, TemplateID: 0,
		})
	}
	_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:"+bp.Key+"/"+mode, 0)
}

// registerBigdataServices 大数据底座服务注册：按勾选组件 + 主/从角色登记服务入口。
func (h *stackHandler) registerBigdataServices(host *model.StackRunHost, params map[string]string) {
	comps := strings.Split(params["components"], ",")
	up := func(name, url string, web bool) {
		_ = h.tplRepo.UpsertHostService(&model.HostService{
			HostID: host.HostID, HostIP: host.HostIP, ServiceName: name,
			URL: url, Web: web, TemplateID: 0,
		})
	}
	for _, c := range comps {
		switch strings.TrimSpace(c) {
		case "hdfs":
			if host.Role == "master" {
				up("HDFS NameNode", "http://"+host.HostIP+":9870", true)
			} else {
				up("HDFS DataNode", "http://"+host.HostIP+":9864", false)
			}
		case "spark":
			webui := params["webui_port"]
			if webui == "" {
				webui = "8080"
			}
			if host.Role == "master" {
				up("Spark Master", "http://"+host.HostIP+":"+webui, true)
			} else {
				up("Spark Worker", "http://"+host.HostIP+":8081", true)
			}
		case "flink":
			if host.Role == "master" {
				up("Flink JobManager", "http://"+host.HostIP+":8081", true)
			} else {
				up("Flink TaskManager", "http://"+host.HostIP+":8081", false)
			}
		case "hive":
			if host.Role == "master" {
				up("HiveServer2", "jdbc:hive2://"+host.HostIP+":10000", false)
				up("Hive Metastore", "thrift://"+host.HostIP+":9083", false)
				up("Hive WebUI", "http://"+host.HostIP+":10002", true)
			}
		case "zookeeper":
			up("ZooKeeper", "zookeeper://"+host.HostIP+":2181", false)
		case "yarn":
			if host.Role == "master" {
				up("YARN ResourceManager", "http://"+host.HostIP+":8088", true)
			} else {
				up("YARN NodeManager", "http://"+host.HostIP+":8042", true)
			}
		case "hbase":
			if host.Role == "master" {
				up("HBase Master", "http://"+host.HostIP+":16010", true)
			} else {
				up("HBase RegionServer", "http://"+host.HostIP+":16030", true)
			}
		case "trino":
			port := params["trino_http_port"]
			if port == "" {
				port = "8080"
			}
			if host.Role == "master" {
				up("Trino Coordinator", "http://"+host.HostIP+":"+port, true)
			} else {
				up("Trino Worker", "http://"+host.HostIP+":"+port, true)
			}
		}
	}
	_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:bigdata/cluster("+params["components"]+")", 0)
}

func applyStackVars(script string, seq int, host model.StackRunHost, extra map[string]string) string {
	rec := repo.HostRecord{DeployTaskHost: model.DeployTaskHost{
		HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
	}}
	out := applyHostVars(script, seq, rec)
	if len(extra) == 0 {
		return out
	}
	pairs := make([]string, 0, len(extra)*2)
	for k, v := range extra {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(out)
}

func stackClusterExtra(hosts []model.StackRunHost) map[int64]map[string]string {
	nodes := make([]string, 0, len(hosts))
	ips := make([]string, 0, len(hosts))
	masterIP, masterPort := "", ""
	hasMasterRole := false
	transport := "9300"
	ports := map[int64]int{}
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		port := strings.TrimSpace(p["port"])
		if port == "" {
			port = "6379"
		}
		n, _ := strconv.Atoi(port)
		ports[h.HostID] = n
		nodes = append(nodes, h.HostIP+":"+port)
		ips = append(ips, h.HostIP)
		if t := strings.TrimSpace(p["transport_port"]); t != "" {
			transport = t
		}
		if h.Role == "master" {
			hasMasterRole = true
			masterIP, masterPort = h.HostIP, port
		}
	}
	if masterIP == "" && len(hosts) > 0 {
		masterIP = hosts[0].HostIP
		masterPort = strconv.Itoa(ports[hosts[0].HostID])
		if masterPort == "0" {
			masterPort = "6379"
		}
	}
	seedParts := make([]string, 0, len(ips))
	for _, ip := range ips {
		seedParts = append(seedParts, strconv.Quote(ip+":"+transport))
	}
	seedHosts := strings.Join(seedParts, ", ")

	out := map[int64]map[string]string{}
	for _, h := range hosts {
		port := ports[h.HostID]
		if port == 0 {
			port = 6379
		}
		boot := h.Role == "master" || (!hasMasterRole && h.Seq == 1)
		bootStr, roles, brokerID := "false", "data", strconv.Itoa(h.Seq)
		if boot {
			bootStr, roles, brokerID = "true", "master, data", "0"
		}
		out[h.HostID] = map[string]string{
			"__nodes":         strings.Join(nodes, ","),
			"__node_ips":      strings.Join(ips, ","),
			"__cluster_size":  strconv.Itoa(len(hosts)),
			"__role":          h.Role,
			"__master_ip":     masterIP,
			"__master_port":   masterPort,
			"__bus_port":      strconv.Itoa(port + 10000),
			"__quorum":        strconv.Itoa(len(hosts)/2 + 1),
			"__is_bootstrap":  bootStr,
			"__join_host":     "rabbit@" + masterIP,
			"__node_name":     "n" + strconv.Itoa(h.Seq),
			"__seed_hosts":    seedHosts,
			"__roles":         roles,
			"__broker_id":     brokerID,
			"__process_roles": "broker,controller",
			"__voter_ips":     strings.Join(ips, ","),
			"__new_nodes":     "",
		}
	}
	return out
}

func stackClusterExtraMerged(op string, existing, newHosts []model.StackRunHost) map[int64]map[string]string {
	byID := map[int64]model.StackRunHost{}
	order := make([]int64, 0, len(existing)+len(newHosts))
	for _, h := range existing {
		if _, ok := byID[h.HostID]; !ok {
			order = append(order, h.HostID)
		}
		byID[h.HostID] = h
	}
	for _, h := range newHosts {
		if _, ok := byID[h.HostID]; !ok {
			order = append(order, h.HostID)
		}
		byID[h.HostID] = h
	}
	all := make([]model.StackRunHost, 0, len(order))
	for _, id := range order {
		all = append(all, byID[id])
	}
	if len(all) == 0 {
		return map[int64]map[string]string{}
	}
	extra := stackClusterExtra(all)
	if op != "scale_out" && op != "add_component" {
		return extra
	}
	newIDs := map[int64]bool{}
	newNodes := make([]string, 0, len(newHosts))
	for _, h := range newHosts {
		newIDs[h.HostID] = true
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		port := strings.TrimSpace(p["port"])
		if port == "" {
			port = "6379"
		}
		newNodes = append(newNodes, h.HostIP+":"+port)
	}
	existIPs := make([]string, 0, len(existing))
	for _, h := range existing {
		existIPs = append(existIPs, h.HostIP)
	}
	newNodesStr := strings.Join(newNodes, ",")
	voterIPs := strings.Join(existIPs, ",")
	for id, e := range extra {
		e["__new_nodes"] = newNodesStr
		if voterIPs != "" {
			e["__voter_ips"] = voterIPs
		}
		if newIDs[id] {
			e["__is_bootstrap"] = "false"
			e["__process_roles"] = "broker"
		}
		extra[id] = e
	}
	return extra
}

func stackVarsJSON(bp model.StackBlueprint, mode string) json.RawMessage {
	vars := make([]tplVar, 0, len(bp.SharedVars)+len(bp.HostVars))
	add := func(list []model.StackVar) {
		for _, v := range list {
			if len(v.Modes) > 0 && !stackVarInMode(v.Modes, mode) {
				continue
			}
			vars = append(vars, tplVar{Name: v.Name, Label: v.Label, Default: v.Default, Required: v.Required})
		}
	}
	add(bp.SharedVars)
	add(bp.HostVars)
	b, _ := json.Marshal(vars)
	return b
}
