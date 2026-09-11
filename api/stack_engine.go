package api

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"infra-ops/api/shared"
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
	// ES 冷热温：角色由每台主机勾选（roles csv，非平台隐式主从），需覆盖通用 extra 中的 __roles/初识 master 推导
	if bp.Key == "elasticsearch" && run.Mode == "cold_warm_hot" {
		extraAll = stackCWHExtraMerged(op, existing, hosts)
	}
	// bigdata：节点脚本里的 {{__master_<comp>}} 角色变量必须在节点阶段注入，否则 NameNode 不会被拉起。
	// 优先使用已物化角色计划（docs/role-plan-design.md）：扩容/加装时运行主机只是成员子集，
	// 现场推导会算错落点，Plan.Injected 是部署渲染的唯一事实源。
	var bigdataPlan *model.RolePlan
	if bp.Key == "bigdata" && run.InstanceID > 0 {
		if inst, ierr := h.repo.GetInstance(run.InstanceID); ierr == nil && inst != nil {
			if p, perr := h.loadRolePlan(inst); perr == nil {
				bigdataPlan = p
			}
		}
	}
	mergeBigdataMasterExtra(bp, hosts, extraAll, bigdataPlan)

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

	// 主脑编排的流水线：套件声明了 ordered 多阶段时，逐阶段调度主机（替代 node→bootstrap 单步）。
	// installPhases 会按运行类型裁剪：create/reinstall 全量、add_component 只跑新增组件、scale_out 只跑节点阶段。
	installPhases := selectPipelinePhases(bp, run, op)
	if len(bp.Pipeline(run.Mode)) > 0 && len(installPhases) == 0 &&
		(op == "add_component" || op == "scale_out") {
		// 声明了流水线的套件，加装/扩容必须走流水线；阶段为空说明请求无效（如未解析到新增组件）。
		// 此处显式失败——若放任回落到 node 阶段，脚本无对应分支，会"秒回 5/5 假成功"却什么都没部署。
		h.failAll(runID, hosts, "未解析到需要执行的流水线阶段（新增组件为空？）")
		return
	}
	if len(installPhases) > 0 {
		ok := h.runPipeline(runID, run.InstanceID, run.Mode, bp, hosts, extraAll, installPhases)
		hosts, _ = h.repo.RunHosts(runID)
		if !ok {
			h.skipRemaining(hosts, false, true)
			h.finish(runID)
			return
		}
		// 流水线仅主节点跑收尾/校验（Target=leader），其余主机 BootstrapStatus 回落到 skipped，避免被判 running
		for i := range hosts {
			if hosts[i].BootstrapStatus == "" || hosts[i].BootstrapStatus == "pending" {
				hosts[i].BootstrapStatus = "skipped"
				_ = h.repo.UpdateHost(&hosts[i])
			}
		}
		h.markHostsDone(hosts)
		h.finish(runID)
		return
	}

	ok := h.runPhaseNode(runID, run.InstanceID, run.Mode, bp, hosts, extraAll, "node", true)
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
	if stackInstallOp(op) && mode.HasBootstrap {
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

func (h *stackHandler) runPhaseNode(runID int64, instanceID int64, mode string, bp *store.BuiltinStack, hosts []model.StackRunHost, extraAll map[int64]map[string]string, phase string, register bool) bool {
	script, err := bp.LoadPhase(mode, "node")
	if err != nil {
		h.appendLog(runID, "node", 0, "", "加载节点脚本失败: "+err.Error())
		return false
	}
	varsJSON := stackVarsJSON(bp.Blueprint(), mode)
	if extraAll == nil {
		extraAll = stackClusterExtra(hosts)
	}
	// 流水线阶段注入 {{__run}}，驱动单脚本 RUN 分发器只执行当前阶段对应的组件。
	// 注意：extraAll 跨阶段共享且为原地修改，必须无条件用当前阶段键覆盖 __run，
	// 否则首个阶段（reset）写入后后续阶段因 "__run 已存在" 被跳过，导致全部阶段跑成 reset。
	if phase != "" {
		for _, m := range extraAll {
			m["__run"] = phase
		}
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
		rendered, rerr := renderScript(script, varsJSON, privatizeImages(params))
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
				if bp.Key == "redis" {
					h.registerRedisService(host, mode, params, instanceID)
				} else {
					h.registerStackService(bp, host, mode, params, instanceID, hosts)
				}
			}
		}
		_ = h.repo.UpdateHost(host)
		h.publishHost(runID, host, "node")
	})
	return atomic.LoadInt32(&failed) == 0
}

// runPipeline 主脑流水线编排器：按顺序逐阶段调度主机，每阶段全部成功才进入下一阶段。
// Target=all 走节点脚本（注入 {{__run}} 驱动 RUN 分发），Target=leader 走 bootstrap 收尾/校验。
func (h *stackHandler) runPipeline(runID, instanceID int64, mode string, bp *store.BuiltinStack, hosts []model.StackRunHost, extraAll map[int64]map[string]string, phases []model.StackPhase) bool {
	for _, step := range phases {
		h.appendLog(runID, "node", 0, "", "【流水线】"+step.Label)
		var ok bool
		if step.Target == "leader" {
			h.runPhaseBootstrap(runID, mode, bp, hosts)
			hosts, _ = h.repo.RunHosts(runID)
			ok = true
			for i := range hosts {
				if hosts[i].BootstrapStatus == "failed" {
					ok = false
					break
				}
			}
		} else {
			ok = h.runPhaseNode(runID, instanceID, mode, bp, hosts, extraAll, step.Key, false)
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
		if bp.Key == "redis" {
			h.registerRedisService(&hosts[i], mode, p, instanceID)
		} else {
			h.registerStackService(bp, &hosts[i], mode, p, instanceID, hosts)
		}
	}
	return true
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
	rendered, rerr := renderScript(script, varsJSON, privatizeImages(params))
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
	if bp.Key == "bigdata" {
		for k, v := range bigdataMasterExtra(hosts) {
			extra[k] = v
		}
	}
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
	// 冷热温缩容：先走官方软下线（排空分片 + 安全退场），失败则中止，避免直接拔容器造成分片丢失
	if op == "scale_in" && run != nil && run.StackKey == "elasticsearch" && run.Mode == "cold_warm_hot" {
		if !h.drainCWHRemoved(runID, run, hosts) {
			h.failAll(runID, hosts, "缩容软下线失败，已中止（未停止任何容器，分片数据未受影响）")
			h.finish(runID)
			return
		}
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

func stackComposeDownScript(home string, comps []string, purge bool) string {
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
	if purge {
		// 清理残留模式：down -v 连同匿名卷/孤儿容器一并移除，并删除 bind-mount 的数据/配置目录
		b.WriteString("down_one() { if [ -f \"$1\" ]; then docker compose -f \"$1\" down -v --remove-orphans || docker compose -f \"$1\" down || true; echo \"已清理 $1\"; fi; }\n")
	} else {
		b.WriteString("down_one() { if [ -f \"$1\" ]; then docker compose -f \"$1\" down || true; echo \"已停止 $1\"; fi; }\n")
	}
	for _, d := range dirs {
		if d == "" {
			b.WriteString("down_one \"${HOME_DIR}/compose.yml\"\n")
			continue
		}
		fmt.Fprintf(&b, "down_one \"${HOME_DIR}/%s/compose.yml\"\n", d)
	}
	if purge {
		// 目录仅限 HOME_DIR 之下，逐个组件目录删除（metadb 嵌套在 hive 下，随 hive 一起删除）
		b.WriteString("rm -rf")
		for _, d := range dirs {
			if d == "" {
				continue
			}
			fmt.Fprintf(&b, " \"${HOME_DIR}/%s\"", d)
		}
		b.WriteString(" \"${HOME_DIR}/compose.yml\"\n")
		b.WriteString("echo \"[Purge] 残留数据/配置目录已删除\"\n")
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
	rendered, rerr := renderScript(script, varsJSON, privatizeImages(params))
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
		// 加装组件是集群级原子变更：只有全部成员都成功才落库，
		// 否则会出现"实例已声明组件、部分机器并未部署"的中间态，且无法重试。
		if op == "add_component" && status != "success" {
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
				comps = shared.MergeStringList(comps, run.Mode)
			} else if stackInstallOp(op) && !shared.ContainsString(comps, run.Mode) {
				comps = shared.MergeStringList(comps, run.Mode)
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
			if op == "add_component" || stackInstallOp(op) {
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
	case op == "add_component":
		// 加装失败/部分成功不影响既有集群可用性：恢复运行前状态，
		// 否则实例会被留在 deploying（失败详情保存在该次运行记录里）
		instStatus = "partial"
		if p := map[string]string{}; json.Unmarshal([]byte(run.ParamsJSON), &p) == nil {
			if prev := strings.TrimSpace(p["prev_instance_status"]); prev != "" && prev != "deploying" {
				instStatus = prev
			}
		}
	case status == "partial":
		instStatus = "partial"
	case stackInstallOp(op):
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

func (h *stackHandler) registerRedisService(host *model.StackRunHost, mode string, params map[string]string, instanceID int64) {
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
		URL: fmt.Sprintf("redis://%s:%s", host.HostIP, port), Web: false, TemplateID: 0, InstanceID: instanceID,
	})
	if mode == "sentinel" {
		sPort := params["sentinel_port"]
		if sPort == "" {
			sPort = "26379"
		}
		_ = h.tplRepo.UpsertHostService(&model.HostService{
			HostID: host.HostID, HostIP: host.HostIP, ServiceName: "Redis Sentinel",
			URL: fmt.Sprintf("redis-sentinel://%s:%s", host.HostIP, sPort), Web: false, TemplateID: 0, InstanceID: instanceID,
		})
	}
	_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:Redis/"+mode, 0)
}

// registerStackService 通用套件服务注册：按套件 Key + 角色登记服务入口与安装台账。
func (h *stackHandler) registerStackService(bp *store.BuiltinStack, host *model.StackRunHost, mode string, params map[string]string, instanceID int64, hosts []model.StackRunHost) {
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
		h.registerBigdataServices(host, params, instanceID, hosts)
		return
	}
	// ES 冷热温：按每台主机勾选角色登记（携带 tier 角色标签），协议随 SSL 开关 http/https
	if bp.Key == "elasticsearch" && mode == "cold_warm_hot" {
		scheme := "http"
		if strings.EqualFold(strings.TrimSpace(params["ssl_enabled"]), "true") {
			scheme = "https"
		}
		bind := func(label string) string {
			_ = h.tplRepo.UpsertHostService(&model.HostService{
				HostID: host.HostID, HostIP: ip, ServiceName: "Elasticsearch-" + label,
				URL: scheme + "://" + ip + ":" + pick("port", "9200"), Web: true,
				TemplateID: 0, InstanceID: instanceID,
			})
			return label
		}
		registered := false
		for _, r := range strings.Split(params["roles"], ",") {
			r = strings.TrimSpace(r)
			switch r {
			case "master":
				bind("master")
			case "coordinator":
				bind("协调")
			case "data_hot":
				bind("数据-hot")
			case "data_warm":
				bind("数据-warm")
			case "data_cold":
				bind("数据-cold")
			}
			if r != "" {
				registered = true
			}
		}
		if !registered {
			bind("节点")
		}
		_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:elasticsearch/"+mode, 0)
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
		URL: svc.url, Web: svc.web, TemplateID: 0, InstanceID: instanceID,
	})
	if bp.Key == "kafka" && mode == "zk" {
		_ = h.tplRepo.UpsertHostService(&model.HostService{
			HostID: host.HostID, HostIP: ip, ServiceName: "ZooKeeper",
			URL: "zookeeper://" + ip + ":" + pick("zk_port", "2181"), Web: false, TemplateID: 0, InstanceID: instanceID,
		})
	}
	_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:"+bp.Key+"/"+mode, 0)
}

// registerBigdataServices 大数据底座服务注册：按勾选组件 + 主/从角色登记服务入口。
// hosts 为本次运行的全部主机（用于确定性从角色判定）；角色矩阵未指定时从角色自动落点。
func (h *stackHandler) registerBigdataServices(host *model.StackRunHost, params map[string]string, instanceID int64, hosts []model.StackRunHost) {
	comps := strings.Split(params["components"], ",")
	r := stackBigdataRole(hosts)
	ip := host.HostIP
	// 角色矩阵：组件→主角色主机 IP；未规划时主角色跟随主节点（Role=="master"）
	masters := map[string]string{}
	_ = json.Unmarshal([]byte(params["masters"]), &masters)
	compMaster := func(comp string) string {
		if m := strings.TrimSpace(masters[comp]); m != "" {
			return m
		}
		if r.Nn1 != "" { // primary 首落点即可
			switch comp {
			case "hdfs":
				return r.Nn1
			case "yarn":
				return r.Rm1
			case "spark":
				return r.SparkM1
			case "flink":
				return r.FlinkJM1
			case "hbase":
				return r.HMaster1
			case "hive":
				return r.MSs[0]
			}
		}
		return ""
	}
	up := func(name, url string, web bool) {
		_ = h.tplRepo.UpsertHostService(&model.HostService{
			HostID: host.HostID, HostIP: host.HostIP, ServiceName: name,
			URL: url, Web: web, TemplateID: 0, InstanceID: instanceID,
		})
	}
	hasJN := func() bool {
		for _, jn := range r.JNs {
			if jn == ip {
				return true
			}
		}
		return false
	}
	inComps := func(c string) bool {
		for _, x := range comps {
			if strings.TrimSpace(x) == c {
				return true
			}
		}
		return false
	}
	for _, c := range comps {
		switch strings.TrimSpace(c) {
		case "hdfs":
			switch {
			case !r.Ha:
				if isMasterAt(ip, compMaster("hdfs")) {
					up("HDFS NameNode", "http://"+ip+":9870", true)
				} else {
					up("HDFS DataNode", "http://"+ip+":9864", false)
				}
			case ip == r.Nn1:
				up("HDFS NameNode", "http://"+ip+":9870", true)
			case ip == r.Nn2:
				up("HDFS NameNode (Standby)", "http://"+ip+":9870", true)
			case hasJN():
				up("HDFS JournalNode", "http://"+ip+":8484", true)
			default:
				up("HDFS DataNode", "http://"+ip+":9864", false)
			}
		case "spark":
			webui := params["webui_port"]
			if webui == "" {
				webui = "8080"
			}
			if r.Ha && ip == r.SparkM2 {
				up("Spark Master (Standby)", "http://"+ip+":"+webui, true)
			} else if isMasterAt(ip, compMaster("spark")) {
				up("Spark Master", "http://"+ip+":"+webui, true)
			} else {
				up("Spark Worker", "http://"+ip+":8081", true)
			}
		case "flink":
			if r.Ha && ip == r.FlinkJm2 {
				up("Flink JobManager (Standby)", "http://"+ip+":8081", true)
			} else if isMasterAt(ip, compMaster("flink")) {
				up("Flink JobManager", "http://"+ip+":8081", true)
			} else {
				up("Flink TaskManager", "http://"+ip+":8081", false)
			}
		case "hive":
			if !inComps("metastore_db") || !r.Ha {
				if isMasterAt(ip, compMaster("hive")) {
					up("HiveServer2", "jdbc:hive2://"+ip+":10000", false)
					up("Hive Metastore", "thrift://"+ip+":9083", false)
					up("Hive WebUI", "http://"+ip+":10002", true)
				}
				continue
			}
			if len(r.Hs2s) > 1 && ip == r.Hs2s[1] {
				up("HiveServer2 (Standby)", "jdbc:hive2://"+ip+":10000", false)
			} else if ip == r.Hs2s[0] {
				up("HiveServer2", "jdbc:hive2://"+ip+":10000", false)
			}
			if len(r.MSs) > 1 && ip == r.MSs[1] {
				up("Hive Metastore (Standby)", "thrift://"+ip+":9083", false)
			} else if ip == r.MSs[0] {
				up("Hive Metastore", "thrift://"+ip+":9083", false)
			}
			if ip == r.HiveDB {
				up("Hive MetaDB (MySQL)", "mysql://"+ip+":3306", false)
			}
		case "zookeeper":
			up("ZooKeeper", "zookeeper://"+ip+":2181", false)
		case "yarn":
			if r.Ha && ip == r.Rm2 {
				up("YARN ResourceManager (Standby)", "http://"+ip+":8088", true)
			} else if isMasterAt(ip, compMaster("yarn")) {
				up("YARN ResourceManager", "http://"+ip+":8088", true)
			} else {
				up("YARN NodeManager", "http://"+ip+":8042", true)
			}
		case "hbase":
			if r.Ha && ip == r.HMaster2 {
				up("HBase HMaster (backup)", "http://"+ip+":16010", true)
			} else if isMasterAt(ip, compMaster("hbase")) {
				up("HBase Master", "http://"+ip+":16010", true)
			} else {
				up("HBase RegionServer", "http://"+ip+":16030", true)
			}
		case "trino":
			port := params["trino_http_port"]
			if port == "" {
				port = "8080"
			}
			if isMasterAt(ip, compMaster("trino")) {
				up("Trino Coordinator", "http://"+ip+":"+port, true)
			} else {
				up("Trino Worker", "http://"+ip+":"+port, true)
			}
		}
	}
	_ = h.tplRepo.MarkHostInstalled(host.HostID, 0, "套件:bigdata/cluster("+params["components"]+")", 0)
}

// isMasterAt 判断主机 IP 是否等于指定主角色 IP（空则否）。
func isMasterAt(ip, masterIP string) bool { return masterIP != "" && ip == masterIP }

// bigdataMasterVars 大数据底座可规划主角色的组件 → 注入脚本的变量名。
var bigdataMasterVars = map[string]string{
	"hdfs":  "__master_hdfs",
	"yarn":  "__master_yarn",
	"spark": "__master_spark",
	"flink": "__master_flink",
	"hive":  "__master_hive",
	"hbase": "__master_hbase",
	"trino": "__master_trino",
}

// bigdataSecondaryVars 组件 → 从角色规划的 masters 参数 key（§4.2）。
// 除 hdfs_jns 为逗号列表外，其余为单个主机 IP。
var bigdataSecondaryVars = map[string]string{
	"hdfs":  "hdfs_nn2",
	"yarn":  "yarn_rm2",
	"spark": "spark_m2",
	"flink": "flink_jm2",
	"hbase": "hbase_hm2",
	"hive":  "hive_ms2",
}

// bigdataRoleKeys 角色规划允许的全部 masters key（主角色 + 从角色 + Hive 额外项）。
var bigdataRoleKeys = func() []string {
	keys := make([]string, 0, len(bigdataMasterVars)+len(bigdataSecondaryVars)+4)
	for k := range bigdataMasterVars {
		keys = append(keys, k)
	}
	for _, k := range bigdataSecondaryVars {
		keys = append(keys, k)
	}
	keys = append(keys, "hdfs_jns", "hive_hs2b", "hive_db", "zookeeper_ips")
	return keys
}()

// stackBigdataMasters 解析 masters 参数（JSON：组件→主角色主机 IP），未指定的组件回落到主节点。
func stackBigdataMasters(hosts []model.StackRunHost) map[string]string {
	primary := ""
	for _, h := range hosts {
		if h.Role == "master" {
			primary = h.HostIP
			break
		}
	}
	if primary == "" && len(hosts) > 0 {
		primary = hosts[0].HostIP
	}
	out := map[string]string{}
	if len(hosts) == 0 {
		return out
	}
	p := map[string]string{}
	_ = json.Unmarshal([]byte(hosts[0].ParamsJSON), &p)
	var override map[string]string
	_ = json.Unmarshal([]byte(p["masters"]), &override)
	for comp := range bigdataMasterVars {
		ip := primary
		if o := strings.TrimSpace(override[comp]); o != "" {
			ip = o
		}
		out[comp] = ip
	}
	return out
}

// bigdataMasterExtra 生成注入变量（__master_<comp> → 主角色 IP），对所有主机相同。
func bigdataMasterExtra(hosts []model.StackRunHost) map[string]string {
	ms := stackBigdataMasters(hosts)
	out := make(map[string]string, len(ms))
	for comp, key := range bigdataMasterVars {
		out[key] = ms[comp]
	}
	return out
}

// bigdataRole 一次 HA 角色分配的确定性结果（§4.1/§4.2）。
type bigdataRole struct {
	Ha                 bool
	ZKIps              string   // ZK ensemble 前 3 台逗号列表
	HdfsEntry          string   // HDFS 入口 URI
	Nn1, Nn2           string   // NameNode Active / Standby
	JNs                []string // JournalNode 主机
	Rm1, Rm2           string   // ResourceManager Active / Standby
	SparkM1, SparkM2   string   // Spark Master 主 / 备
	FlinkJM1, FlinkJm2 string   // Flink JobManager 主 / 备
	HMaster1, HMaster2 string   // HBase HMaster 主 / backup
	MSs, Hs2s          []string // Hive Metastore / HiveServer2 主机
	HiveDB             string
}

// stackBigdataRole 按 §4.2 确定性算法解析大数据底座全部角色落点。
// 读取 hosts[0].ParamsJSON 中的 ha/masters 参数；hosts 需按 Seq 有序。
// primary = 角色矩阵指定 ?? 主节点；secondary = 角色矩阵指定 ?? seq 最小且非 primary 的主机；
// jn/zk = 角色矩阵指定 ?? 前 3 台；hive_db_host = 角色矩阵指定 ?? primary(hive) 主机。
func stackBigdataRole(hosts []model.StackRunHost) bigdataRole {
	res := bigdataRole{}
	if len(hosts) == 0 {
		return res
	}
	sorted := make([]model.StackRunHost, len(hosts))
	copy(sorted, hosts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	ips := make([]string, 0, len(sorted))
	for _, h := range sorted {
		ips = append(ips, h.HostIP)
	}
	p := map[string]string{}
	_ = json.Unmarshal([]byte(sorted[0].ParamsJSON), &p)
	res.Ha = strings.EqualFold(strings.TrimSpace(p["ha"]), "true")
	override := map[string]string{}
	_ = json.Unmarshal([]byte(p["masters"]), &override)

	primary := ""
	for _, h := range sorted {
		if h.Role == "master" {
			primary = h.HostIP
			break
		}
	}
	if primary == "" {
		primary = ips[0]
	}
	masterOf := func(comp string) string {
		if ip := strings.TrimSpace(override[comp]); ip != "" {
			return ip
		}
		return primary
	}
	// seq 最小的非 primary 主机（component 从角色自动落点）
	autoSecondary := func(comp string) string {
		for _, ip := range ips {
			if ip != masterOf(comp) {
				return ip
			}
		}
		return ""
	}
	secondaryOf := func(comp, key string) string {
		if ip := strings.TrimSpace(override[key]); ip != "" {
			return ip
		}
		if res.Ha {
			return autoSecondary(comp)
		}
		return ""
	}
	firstN := func(n int) []string {
		if len(ips) > n {
			return ips[:n]
		}
		return ips
	}

	zk := firstN(3)
	if s := strings.TrimSpace(override["zookeeper_ips"]); s != "" {
		zk = shared.FilterEmpty(strings.Split(s, ","))
	}
	res.ZKIps = strings.Join(zk, ",")

	nn1 := masterOf("hdfs")
	nn2 := secondaryOf("hdfs", "hdfs_nn2")
	jns := firstN(3)
	if s := strings.TrimSpace(override["hdfs_jns"]); s != "" {
		jns = shared.FilterEmpty(strings.Split(s, ","))
	}
	res.Nn1, res.Nn2 = nn1, nn2
	res.JNs = jns

	ns := strings.TrimSpace(p["hdfs_nameservice"])
	if ns == "" {
		ns = "ns1"
	}
	if res.Ha {
		res.HdfsEntry = "hdfs://" + ns
	} else {
		rpc := strings.TrimSpace(p["nn_rpc_port"])
		if rpc == "" {
			rpc = "9000"
		}
		res.HdfsEntry = "hdfs://" + nn1 + ":" + rpc
	}

	res.Rm1 = masterOf("yarn")
	res.Rm2 = secondaryOf("yarn", "yarn_rm2")
	res.SparkM1 = masterOf("spark")
	res.SparkM2 = secondaryOf("spark", "spark_m2")
	res.FlinkJM1 = masterOf("flink")
	res.FlinkJm2 = secondaryOf("flink", "flink_jm2")
	res.HMaster1 = masterOf("hbase")
	res.HMaster2 = secondaryOf("hbase", "hbase_hm2")

	hiveP := masterOf("hive")
	res.HiveDB = strings.TrimSpace(override["hive_db"])
	if res.HiveDB == "" {
		res.HiveDB = hiveP
	}
	// Metastore 双实例：ms1=primary，ms2=矩阵指定 ?? 自动第二台
	ms2 := secondaryOf("hive", "hive_ms2")
	res.MSs = []string{hiveP}
	if ms2 != "" && ms2 != hiveP {
		res.MSs = []string{hiveP, ms2}
	}
	// HiveServer2 双实例：独立落点键 hive_hs2b
	res.Hs2s = res.MSs
	if s := strings.TrimSpace(override["hive_hs2b"]); s != "" && s != hiveP {
		res.Hs2s = []string{hiveP, s}
	}
	return res
}

// bigdataHAVarTable 将 HA 角色分配结果转为注入变量表（§4.1，非 HA 组件位一律注入空串）。
func bigdataHAVarTable(r bigdataRole) map[string]string {
	jnStr := strings.Join(r.JNs, ",")
	msUris := make([]string, 0, len(r.MSs))
	for _, m := range r.MSs {
		msUris = append(msUris, "thrift://"+m+":9083")
	}
	return map[string]string{
		"__zk_ips":       r.ZKIps,
		"__hdfs_entry":   r.HdfsEntry,
		"__nn1_ip":       r.Nn1,
		"__nn2_ip":       r.Nn2,
		"__jn_ips":       jnStr,
		"__rm1_ip":       r.Rm1,
		"__rm2_ip":       r.Rm2,
		"__spark_m2_ip":  r.SparkM2,
		"__flink_jm2_ip": r.FlinkJm2,
		"__hmaster2_ip":  r.HMaster2,
		"__hs2_ips":      strings.Join(r.Hs2s, ","),
		"__ms_ips":       strings.Join(r.MSs, ","),
		"__hive_ms_uris": strings.Join(msUris, ","),
		"__hive_db_ip":   r.HiveDB,
	}
}

// xmlProp 生成单个 Hadoop/云原生 XML property 块（值做 XML 转义）。
func xmlProp(name, value string) string {
	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", `'`, "&apos;")
		return r.Replace(s)
	}
	return "  <property>\n    <name>" + esc(name) + "</name>\n    <value>" + esc(value) + "</value>\n  </property>\n"
}

// bigdataHAXMLBlocks 生成配置模板中以 {{__ha_*}} 注入的 XML 条件块（§8.2）。
// 非 HA / 未选组件一律返回空串或与现状一致的单实例值，保证非 HA 渲染零差异。
func bigdataHAXMLBlocks(r bigdataRole, params map[string]string) map[string]string {
	out := map[string]string{
		"__ha_core_props": "", "__ha_hdfs_props": "", "__ha_yarn_props": "",
		"__ha_hive_jdo": "", "__ha_hive_ms": "", "__ha_hive_zk": "",
	}
	ns := strings.TrimSpace(params["hdfs_nameservice"])
	if ns == "" {
		ns = "ns1"
	}
	nnRpc := strings.TrimSpace(params["nn_rpc_port"])
	if nnRpc == "" {
		nnRpc = "9000"
	}
	jnRpc := strings.TrimSpace(params["jn_rpc_port"])
	if jnRpc == "" {
		jnRpc = "8485"
	}
	compOn := func(c string) bool {
		for _, x := range parseComponentsCSV(params["components"]) {
			if strings.EqualFold(strings.TrimSpace(x), c) {
				return true
			}
		}
		return false
	}

	if r.Ha {
		var b strings.Builder
		b.WriteString(xmlProp("ha.zookeeper.quorum", r.ZKIps))
		b.WriteString(xmlProp("dfs.nameservices", ns))
		b.WriteString(xmlProp("dfs.ha.namenodes."+ns, "nn1,nn2"))
		b.WriteString(xmlProp("dfs.client.failover.proxy.provider."+ns, "org.apache.hadoop.hdfs.server.namenode.ha.ConfiguredFailoverProxyProvider"))
		out["__ha_core_props"] = b.String()

		b.Reset()
		b.WriteString(xmlProp("dfs.namenode.shared.edits.dir", "qjournal://"+strings.Join(r.JNs, ":"+jnRpc+";")+":"+jnRpc+"/"+ns))
		b.WriteString(xmlProp("dfs.journalnode.edits.dir", "/hadoop/dfs/journal"))
		b.WriteString(xmlProp("dfs.namenode.rpc-address."+ns+".nn1", r.Nn1+":"+nnRpc))
		b.WriteString(xmlProp("dfs.namenode.rpc-address."+ns+".nn2", r.Nn2+":"+nnRpc))
		b.WriteString(xmlProp("dfs.namenode.http-address."+ns+".nn1", r.Nn1+":9870"))
		b.WriteString(xmlProp("dfs.namenode.http-address."+ns+".nn2", r.Nn2+":9870"))
		b.WriteString(xmlProp("dfs.ha.automatic-failover.enabled", "true"))
		b.WriteString(xmlProp("dfs.ha.fencing.methods", "shell(/bin/true)"))
		// 容器内主机名为 IP 字面量时默认反解失败会拒收 DataNode 注册，需关闭该检查
		b.WriteString(xmlProp("dfs.namenode.datanode.registration.ip-hostname-check", "false"))
		out["__ha_hdfs_props"] = b.String()

		if compOn("yarn") {
			b.Reset()
			b.WriteString(xmlProp("yarn.resourcemanager.ha.enabled", "true"))
			b.WriteString(xmlProp("yarn.resourcemanager.ha.rm-ids", "rm1,rm2"))
			b.WriteString(xmlProp("yarn.resourcemanager.hostname.rm1", r.Rm1))
			b.WriteString(xmlProp("yarn.resourcemanager.hostname.rm2", r.Rm2))
			// HA 下 MR AppMaster 的 AmFilterInitializer 会读取 yarn.resourcemanager.webapp.address.<rmId>
			// 拼装 RM_HA_URLS；若缺失则该值为 null，StringUtils.join 直接抛 NPE，
			// 导致 AM WebApp 启动失败 → MRClientService.getHttpPort() NPE → AM exit 1。
			b.WriteString(xmlProp("yarn.resourcemanager.webapp.address.rm1", r.Rm1+":8088"))
			b.WriteString(xmlProp("yarn.resourcemanager.webapp.address.rm2", r.Rm2+":8088"))
			b.WriteString(xmlProp("yarn.resourcemanager.cluster-id", "bigdata-yarn"))
			b.WriteString(xmlProp("yarn.resourcemanager.zk-address", r.ZKIps))
			b.WriteString(xmlProp("yarn.resourcemanager.recovery.enabled", "true"))
			b.WriteString(xmlProp("yarn.resourcemanager.store.class", "org.apache.hadoop.yarn.server.resourcemanager.recovery.ZKRMStateStore"))
			b.WriteString(xmlProp("yarn.resourcemanager.ha.automatic-failover.enabled", "true"))
			b.WriteString(xmlProp("yarn.client.failover-proxy-provider", "org.apache.hadoop.yarn.client.ConfiguredRMFailoverProxyProvider"))
			out["__ha_yarn_props"] = b.String()
		}

		if compOn("hive") && len(r.MSs) > 0 {
			msList := make([]string, 0, len(r.MSs))
			for _, m := range r.MSs {
				msList = append(msList, "thrift://"+m+":9083")
			}
			out["__ha_hive_ms"] = strings.Join(msList, ",")
			pw := strings.TrimSpace(params["hive_db_password"])
			if pw == "" {
				pw = "HiveDb@123"
			}
			b.Reset()
			b.WriteString(xmlProp("javax.jdo.option.ConnectionURL", "jdbc:mysql://"+r.HiveDB+":3306/metastore?useSSL=false&allowPublicKeyRetrieval=true&characterEncoding=UTF-8"))
			b.WriteString(xmlProp("javax.jdo.option.ConnectionDriverName", "com.mysql.cj.jdbc.Driver"))
			b.WriteString(xmlProp("javax.jdo.option.ConnectionUserName", "root"))
			b.WriteString(xmlProp("javax.jdo.option.ConnectionPassword", pw))
			out["__ha_hive_jdo"] = b.String()
			b.Reset()
			b.WriteString(xmlProp("hive.server2.support.dynamic.service.discovery", "true"))
			b.WriteString(xmlProp("hive.zookeeper.quorum", r.ZKIps))
			b.WriteString(xmlProp("hive.zookeeper.namespace", "hiveserver2"))
			out["__ha_hive_zk"] = b.String()
		}
	}
	if !r.Ha || !compOn("hive") {
		// 非 HA / 未选 Hive：保留 Derby 单实例元数据连接（与现状一致）
		out["__ha_hive_jdo"] = xmlProp("javax.jdo.option.ConnectionURL", "jdbc:derby:;databaseName=/opt/hive/data/derby;create=true")
		out["__ha_hive_zk"] = ""
		if compOn("hive") && len(r.MSs) > 0 {
			out["__ha_hive_ms"] = "thrift://" + r.MSs[0] + ":9083"
		} else {
			out["__ha_hive_ms"] = ""
		}
	}
	return out
}

// mergeBigdataMasterExtra 将大数据底座主/从角色变量并入 per-host 注入表。
// 存在已物化 Plan 时直接取 plan.Injected（唯一事实源）；否则按旧路径现场推导（兜底）。
func mergeBigdataMasterExtra(bp *store.BuiltinStack, hosts []model.StackRunHost, extraAll map[int64]map[string]string, plan *model.RolePlan) {
	if bp == nil || bp.Key != "bigdata" || len(extraAll) == 0 {
		return
	}
	if plan != nil && len(plan.Injected) > 0 {
		for id, extra := range extraAll {
			if extra == nil {
				extra = map[string]string{}
				extraAll[id] = extra
			}
			for k, v := range plan.Injected {
				extra[k] = v
			}
		}
		return
	}
	m := bigdataMasterExtra(hosts)
	role := stackBigdataRole(hosts)
	ha := bigdataHAVarTable(role)
	params := map[string]string{}
	if len(hosts) > 0 {
		_ = json.Unmarshal([]byte(hosts[0].ParamsJSON), &params)
	}
	for k, v := range bigdataHAXMLBlocks(role, params) {
		ha[k] = v
	}
	for id, extra := range extraAll {
		if extra == nil {
			extra = map[string]string{}
			extraAll[id] = extra
		}
		for k, v := range m {
			extra[k] = v
		}
		for k, v := range ha {
			extra[k] = v
		}
	}
}

// privatizeImages 在参数中提供了 image_registry（内网/私有镜像仓库前缀，如 192.168.7.13:5000）时，
// 把 image / image_* / *_image 类镜像参数统一改写为 <前缀>/<仓库>:<标签>，便于离线内网部署；
// 未提供前缀时原样返回，不影响既有行为。
func privatizeImages(params map[string]string) map[string]string {
	reg := strings.TrimSuffix(strings.TrimSpace(params["image_registry"]), "/")
	if reg == "" {
		return params
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = v
	}
	for k, v := range out {
		if !isImageParam(k) {
			continue
		}
		img := strings.TrimSpace(v)
		if img == "" {
			continue
		}
		out[k] = reg + "/" + trimRegistryPrefix(img)
	}
	return out
}

func isImageParam(k string) bool {
	if k == "image_registry" {
		return false
	}
	return k == "image" || strings.HasPrefix(k, "image_") || strings.HasSuffix(k, "_image")
}

// trimRegistryPrefix 去掉镜像地址中已存在的 registry 段（首段含 . 或 : 即视为 registry），
// 保证前缀重复应用时不会叠加成 a:5000/a:5000/img。
func trimRegistryPrefix(img string) string {
	parts := strings.SplitN(img, "/", 2)
	if len(parts) == 2 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":")) {
		return parts[1]
	}
	return img
}

func applyStackVars(script string, seq int, host model.StackRunHost, extra map[string]string) string {
	rec := repo.HostRecord{DeployTaskHost: model.DeployTaskHost{
		HostID: host.HostID, HostName: host.HostName, HostIP: host.HostIP,
	}}
	out := shared.ApplyHostVars(script, seq, rec)
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

// cwhNodeRoles 将冷热温每台主机的 roles csv 映射为 ES node.roles 列表体（yml 用）。
// master→"master"；纯协调(coordinator)→空（node.roles: [] 为纯协调）；数据层→按勾选叠加 data_hot/warm/cold。
func cwhNodeRoles(rolesCSV string) string {
	var parts []string
	for _, r := range strings.Split(rolesCSV, ",") {
		r = strings.TrimSpace(r)
		switch r {
		case "master":
			return `"master"`
		case "coordinator":
			// 纯协调：不落 data，node.roles 为空数组
		default:
			if r != "" {
				parts = append(parts, strconv.Quote(r))
			}
		}
	}
	return strings.Join(parts, ",")
}

// stackCWHExtra 冷热温专用注入：以每台主机 roles 参数为权威来源派生 node.roles 与初识 master 变量，
// 其余通用变量（seed_hosts/node_name/cluster_size 等）复用 stackClusterExtra。
func stackCWHExtra(hosts []model.StackRunHost) map[int64]map[string]string {
	base := stackClusterExtra(hosts)
	bootName, bootSeq := "", 0
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		if strings.Contains(p["roles"], "master") && (bootSeq == 0 || h.Seq < bootSeq) {
			bootSeq, bootName = h.Seq, "n"+strconv.Itoa(h.Seq)
		}
	}
	for _, h := range hosts {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(h.ParamsJSON), &p)
		isMaster := strings.Contains(p["roles"], "master")
		name := "n" + strconv.Itoa(h.Seq)
		m := base[h.HostID]
		m["__es_roles"] = cwhNodeRoles(p["roles"])
		m["__es_is_master"] = strconv.FormatBool(isMaster)
		m["__es_bootstrap"] = strconv.FormatBool(bootName != "" && name == bootName)
		m["__es_bootstrap_name"] = bootName
		base[h.HostID] = m
	}
	return base
}

func stackCWHExtraMerged(op string, existing, newHosts []model.StackRunHost) map[int64]map[string]string {
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
	return stackCWHExtra(all)
}

func stackInstallOp(op string) bool {
	return op == "create" || op == "reinstall"
}

// selectPipelinePhases 按运行类型裁剪套件流水线阶段：
//   - create/reinstall：整机部署，全量执行；
//   - add_component：只执行本次新增组件对应的阶段（Always 阶段恒执行），
//     并跳过 FullOnly 阶段（典型是 reset，否则"加装"会把既有集群数据清空）；
//   - scale_out：新节点入列，执行各组件阶段，但跳过 FullOnly（reset）
//     与 Target=leader 的收尾阶段（bootstrap 必须在元老主节点上跑）。
//
// 返回 nil 表示本次操作不走流水线（交由调用方回落到 node→bootstrap 路径）。
func selectPipelinePhases(bp *store.BuiltinStack, run *model.StackRun, op string) []model.StackPhase {
	all := bp.Pipeline(run.Mode)
	if len(all) == 0 {
		return nil
	}
	switch op {
	case "create", "reinstall":
		return all
	case "add_component":
		p := map[string]string{}
		_ = json.Unmarshal([]byte(run.ParamsJSON), &p)
		added := parseComponentsCSV(p["add_components"])
		if len(added) == 0 {
			// 兼容早期未落 add_components 的历史运行：退回"合并后组件 − 已安装组件"的差集由调用方判定为空
			return nil
		}
		want := map[string]bool{}
		for _, c := range added {
			want[strings.TrimSpace(c)] = true
		}
		out := make([]model.StackPhase, 0, len(all))
		for _, ph := range all {
			if ph.FullOnly {
				continue
			}
			if ph.Always || phaseHasComponent(ph, want) {
				out = append(out, ph)
			}
		}
		return out
	case "scale_out":
		out := make([]model.StackPhase, 0, len(all))
		for _, ph := range all {
			if ph.FullOnly || ph.Target == "leader" {
				continue
			}
			out = append(out, ph)
		}
		return out
	}
	return nil
}

// phaseHasComponent 判断阶段是否归属给定组件集合（Component 为逗号分隔的组件名列表）。
func phaseHasComponent(ph model.StackPhase, want map[string]bool) bool {
	for _, c := range strings.Split(ph.Component, ",") {
		c = strings.TrimSpace(c)
		if c != "" && want[c] {
			return true
		}
	}
	return false
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

// drainCWHRemoved 冷热温缩容软下线：在存活 leader 上先排空待下线节点分片、安全退场，
// 集群回 green 后再放行停容器。返回 false 表示软下线失败（中止缩容，保护分片数据）。
// 非冷热温模式直接返回 true（走通用直接停服路径）。
func (h *stackHandler) drainCWHRemoved(runID int64, run *model.StackRun, removed []model.StackRunHost) bool {
	if run == nil || len(removed) == 0 {
		return true
	}
	bp := store.FindBuiltinStack(run.StackKey)
	if bp == nil || bp.Key != "elasticsearch" || run.Mode != "cold_warm_hot" {
		return true
	}
	script, err := bp.LoadPhase(run.Mode, "scale_in")
	if err != nil {
		h.appendLog(runID, "node", 0, "", "缩容下线脚本缺失，降级为直接停服: "+err.Error())
		return true
	}
	removedIDs := map[int64]bool{}
	var removeNodes, removeMasters []string
	for i := range removed {
		name := "n" + strconv.Itoa(removed[i].Seq)
		removeNodes = append(removeNodes, name)
		removedIDs[removed[i].HostID] = true
		p := map[string]string{}
		_ = json.Unmarshal([]byte(removed[i].ParamsJSON), &p)
		if strings.Contains(p["roles"], "master") {
			removeMasters = append(removeMasters, name)
		}
	}
	sort.Slice(removeNodes, func(a, b int) bool { return removeNodes[a] < removeNodes[b] })
	sort.Slice(removeMasters, func(a, b int) bool { return removeMasters[a] < removeMasters[b] })

	// 选存活 leader：master 优先，否则首个存活成员；全被移除则无存活，跳过软下线
	var survivors []model.StackRunHost
	if run.InstanceID > 0 {
		if instHosts, ierr := h.repo.InstanceHosts(run.InstanceID, true); ierr == nil {
			for _, ih := range instHosts {
				if removedIDs[ih.HostID] {
					continue
				}
				rh := model.StackRunHost{HostID: ih.HostID, HostName: ih.HostName, HostIP: ih.HostIP, Seq: ih.Seq, ParamsJSON: ih.ParamsJSON}
				p := map[string]string{}
				_ = json.Unmarshal([]byte(ih.ParamsJSON), &p)
				if strings.Contains(p["roles"], "master") {
					survivors = append([]model.StackRunHost{rh}, survivors...)
				} else {
					survivors = append(survivors, rh)
				}
			}
		}
	}
	if len(survivors) == 0 {
		h.appendLog(runID, "node", 0, "", "无存活成员，跳过软下线直接停服")
		return true
	}
	leader := &survivors[0]

	params := map[string]string{}
	_ = json.Unmarshal([]byte(leader.ParamsJSON), &params)
	params["self_ip"] = leader.HostIP
	params["remove_nodes"] = strings.Join(removeNodes, ",")
	params["remove_masters"] = strings.Join(removeMasters, ",")
	varsJSON := stackVarsJSON(bp.Blueprint(), run.Mode)
	rendered, rerr := renderScript(script, varsJSON, privatizeImages(params))
	if rerr != nil {
		h.appendLog(runID, "node", leader.HostID, leader.HostIP, "缩容下线脚本渲染失败: "+rerr.Error())
		return false
	}
	h.appendLog(runID, "node", leader.HostID, leader.HostIP,
		fmt.Sprintf("软下线开始：在 %s 排空节点 %s", leader.HostIP, strings.Join(removeNodes, ",")))
	target := *leader
	target.RunID = runID
	target.Output = ""
	out, execErr := h.execScript(&target, rendered, "node")
	if execErr != nil {
		leader.Output = out
		h.appendLog(runID, "node", leader.HostID, leader.HostIP, "软下线失败，中止缩容: "+execErr.Error())
		_ = h.repo.UpdateHost(leader)
		return false
	}
	h.appendLog(runID, "node", leader.HostID, leader.HostIP, "软下线完成，集群已排空回绿，可安全停服")
	return true
}
