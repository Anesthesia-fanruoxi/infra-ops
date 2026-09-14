package stack

import (
	"encoding/json"
	"sync/atomic"

	"infra-ops/api/deploy"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// skipPhasePrereq 主机安装类套件直接跳过 Docker 前置检查。
func (h *stackHandler) skipPhasePrereq(runID int64, hosts []model.StackRunHost) {
	h.appendLog(runID, "prereq", 0, "", "该套件为主机安装，跳过 Docker 检查")
	for i := range hosts {
		host := &hosts[i]
		host.PrereqStatus = "skipped"
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "prereq")
	}
}

// runPhasePrereq 逐主机检查并安装 Docker（已装则跳过不改动）。
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

// runPhaseNode 逐主机执行节点脚本；流水线模式下注入 {{__run}} 驱动单脚本 RUN 分发器。
func (h *stackHandler) runPhaseNode(runID int64, instanceID int64, mode string, d stackkit.Driver, hosts []model.StackRunHost, extraAll map[int64]map[string]string, phase string, register bool) bool {
	script, err := store.LoadStackPhase(d, mode, "node")
	if err != nil {
		h.appendLog(runID, "node", 0, "", "加载节点脚本失败: "+err.Error())
		return false
	}
	varsJSON := stackVarsJSON(d.Blueprint(), mode)
	if extraAll == nil {
		extraAll = stackkit.ClusterExtra(hosts)
	}
	// 流水线阶段注入 {{__run}}，驱动单脚本 RUN 分发器只执行当前阶段对应的组件。
	// 注意：extraAll 跨阶段共享且为原地修改，必须无条件用当前阶段键覆盖 __run，
	// 否则首个阶段（reset）写入后后续阶段因 "__run 已存在" 被跳过，导致全部阶段跑成 reset。
	if phase != "" {
		for _, m := range extraAll {
			m["__run"] = phase
		}
	}
	h.appendLog(runID, "node", 0, "", "开始部署 "+d.Blueprint().Name+" 节点")
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
		rendered, rerr := deploy.RenderScript(script, varsJSON, privatizeImages(params))
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
			if register {
				h.registerStackService(d, host, mode, params, instanceID, hosts)
			}
		}
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "node")
	})
	return atomic.LoadInt32(&failed) == 0
}

// runPipeline 主脑流水线编排器：按顺序逐阶段调度主机，每阶段全部成功才进入下一阶段。
// Target=all 走节点脚本（注入 {{__run}} 驱动 RUN 分发），Target=leader 走 bootstrap 收尾/校验。
func (h *stackHandler) runPipeline(runID, instanceID int64, mode string, d stackkit.Driver, hosts []model.StackRunHost, extraAll map[int64]map[string]string, phases []model.StackPhase) bool {
	for _, step := range phases {
		h.appendLog(runID, "node", 0, "", "【流水线】"+step.Label)
		var ok bool
		if step.Target == "leader" {
			h.runPhaseBootstrap(runID, mode, d, hosts)
			hosts, _ = h.repo.RunHosts(runID)
			ok = true
			for i := range hosts {
				if hosts[i].BootstrapStatus == "failed" {
					ok = false
					break
				}
			}
		} else {
			ok = h.runPhaseNode(runID, instanceID, mode, d, hosts, extraAll, step.Key, false)
			hosts, _ = h.repo.RunHosts(runID)
		}
		if !ok {
			h.appendLog(runID, "node", 0, "", "阶段未通过，终止流水线: "+step.Label)
			return false
		}
	}
	// 流水线全成功后再统一注册主机服务，供探活与 UI 展示
	hosts, _ = h.repo.RunHosts(runID)
	for i := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(hosts[i].ParamsJSON), &p)
		h.registerStackService(d, &hosts[i], mode, p, instanceID, hosts)
	}
	return true
}

// runPhaseBootstrap 在主节点上执行集群初始化/收尾脚本。
func (h *stackHandler) runPhaseBootstrap(runID int64, mode string, d stackkit.Driver, hosts []model.StackRunHost) {
	script, err := store.LoadStackPhase(d, mode, "bootstrap")
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
	varsJSON := stackVarsJSON(d.Blueprint(), mode)
	// 运行期默认值兜底（如 Redis 集群 replicas 缺省按 0）：套件只在其原本生效的时点补全
	if dp, ok := d.(stackkit.DefaultsProvider); ok {
		if derr := dp.Defaults("bootstrap", mode, params); derr != nil {
			h.appendLog(runID, "bootstrap", 0, "", "参数默认值补全失败: "+derr.Error())
		}
	}
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
	// 集群变量：通用表 + 套件增补（大数据底座的主从落点 {{__master_*}} 必须在 bootstrap 阶段可见，
	// 否则集群初始化脚本找不到 NameNode/ResourceManager 等主角色主机）
	extraAll := stackkit.ClusterExtra(hosts)
	if vi, ok := d.(stackkit.VarInjector); ok {
		if out := vi.ExtraVars(stackkit.VarsCtx{
			Mode: mode, Op: "bootstrap", Hosts: hosts, Extra: extraAll,
		}); out != nil {
			extraAll = out
		}
	}
	extra := extraAll[leader.HostID]
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
