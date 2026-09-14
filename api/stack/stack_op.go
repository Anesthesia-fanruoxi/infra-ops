package stack

import (
	"encoding/json"
	"strings"

	"infra-ops/api/deploy"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// runPhaseRemove 处置缩容 / 卸载 / 加装前移除组件的停止与清理路径。
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
	purge := false
	{
		p := map[string]string{}
		_ = json.Unmarshal([]byte(run.ParamsJSON), &p)
		if p["purge"] == "true" {
			purge = true
			lead = "开始清理残留：停止并删除全部节点上的套件容器与数据/配置目录"
		}
	}
	if lead == "" {
		lead = "开始停止套件服务"
	}
	h.appendLog(runID, "node", 0, "", lead)
	// 缩容软下线（可选能力）：套件声明本模式需先排空待下线节点再停服；失败则中止，
	// 避免直接拔容器造成分片/数据丢失。
	if op == "scale_in" && run != nil && !h.runScaleInDrain(runID, run, hosts) {
		h.failAll(runID, hosts, "缩容软下线失败，已中止（未停止任何容器，分片数据未受影响）")
		h.finish(runID)
		return
	}
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
		script := stackComposeDownScript(home, removeComps, purge)
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

// runPhaseScaleOut 扩容时在元老主节点上把新节点加入现有集群。
func (h *stackHandler) runPhaseScaleOut(runID int64, mode string, d stackkit.Driver, newHosts, existing []model.StackRunHost, extraAll map[int64]map[string]string) {
	script, err := store.LoadStackPhase(d, mode, "scale_out")
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
	varsJSON := stackVarsJSON(d.Blueprint(), mode)
	rendered, rerr := deploy.RenderScript(script, varsJSON, privatizeImages(params))
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
