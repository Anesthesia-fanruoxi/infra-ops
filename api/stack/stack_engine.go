package stack

import (
	"strings"

	"infra-ops/common/eventbus"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// execute 套件运行入口：按运行类型分发到 per-host 节点阶段、主脑流水线或卸载/缩容/加装路径。
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
	d := store.FindBuiltinStack(run.StackKey)
	if d == nil {
		h.failAll(runID, hosts, "套件蓝图不存在")
		return
	}
	mode := stackkit.ModeDef(d.Blueprint(), run.Mode)
	if mode == nil {
		h.failAll(runID, hosts, "未知模式")
		return
	}
	op := strings.TrimSpace(run.Op)
	if op == "" {
		op = "create"
	}

	if op == "scale_in" || op == "uninstall" || op == "remove_component" {
		for _, s := range buildRunSteps(op, d, mode, nil, "") {
			_ = h.repo.CreateRunSteps(runID, []model.StackRunStep{s})
		}
		h.stepRunning(runID, "remove")
		h.runPhaseRemove(runID, run, hosts)
		hosts, _ = h.repo.RunHosts(runID)
		h.stepDone(runID, "remove", !anyHostFailed(hosts))
		h.finish(runID)
		return
	}

	existing := []model.StackRunHost{}
	if run.InstanceID > 0 && (op == "scale_out" || op == "add_component") {
		if instHosts, err := h.repo.InstanceHosts(run.InstanceID, true); err == nil {
			existing = instanceHostsAsRunHosts(instHosts)
		}
	}
	extraAll := h.runClusterExtra(d, run, op, existing, hosts)

	// 步骤清单一次性物化（在 prereq 之前，Docker 环境步骤的状态推进才有载体）
	installPhases := selectPipelinePhases(d, run, op)
	runSteps := buildRunSteps(op, d, mode, installPhases, h.runStepComponents(run))
	// hub 镜像源模式：Docker 套件且参数带 __hub_host_id 时，prereq 之后插入镜像源预热步骤
	hubHostID, _ := hubCfgFromRun(run.ParamsJSON)
	useHub := hubHostID > 0 && d.Blueprint().RequiresDocker && hubInstallOps[op]
	if useHub {
		runSteps = insertHubStep(runSteps)
	}
	_ = h.repo.CreateRunSteps(runID, runSteps)

	if d.Blueprint().RequiresDocker {
		h.stepRunning(runID, "prereq")
		ok := h.runPhasePrereq(runID, hosts)
		h.stepDone(runID, "prereq", ok)
		hosts, _ = h.repo.RunHosts(runID)
		if !ok {
			h.skipRemaining(runID, hosts, true, true)
			h.finish(runID)
			return
		}
	} else {
		h.skipPhasePrereq(runID, hosts)
		hosts, _ = h.repo.RunHosts(runID)
	}

	// hub 镜像源阶段：hub 健康校验 → 镜像预热 → 目标机 insecure 信任预检（串行，
	// 复用部署模板引擎的台账解析与脚本口径）；任一失败严格中止，不回退直连拉取
	if useHub {
		h.stepRunning(runID, "hub")
		h.appendLog(runID, "hub", 0, "", "开始 hub 镜像源校验与预热")
		ok := h.runPhaseHub(runID, run, hosts)
		h.stepDone(runID, "hub", ok)
		hosts, _ = h.repo.RunHosts(runID)
		if !ok {
			h.skipRemaining(runID, hosts, true, true)
			h.finish(runID)
			return
		}
	}

	// 主脑编排的流水线：套件声明了 ordered 多阶段时，逐阶段调度主机（替代 node→bootstrap 单步）。
	// installPhases 会按运行类型裁剪：create/reinstall 全量、add_component 只跑新增组件、scale_out 只跑节点阶段。
	if len(d.Pipeline(run.Mode)) > 0 && len(installPhases) == 0 &&
		(op == "add_component" || op == "scale_out") {
		// 声明了流水线的套件，加装/扩容必须走流水线；阶段为空说明请求无效（如未解析到新增组件）。
		// 此处显式失败——若放任回落到 node 阶段，脚本无对应分支，会"秒回 5/5 假成功"却什么都没部署。
		h.failAll(runID, hosts, "未解析到需要执行的流水线阶段（新增组件为空？）")
		return
	}
	// 离线资产分发：套件声明（AssetProvisioner）且服务端已就绪的资产经 SFTP 推送到各主机，
	// 部署脚本优先使用已就位文件（存在性判断），未就绪则回落脚本内建下载
	h.provisionAssets(runID, run, d, mode, hosts)

	if len(installPhases) > 0 {
		ok := h.runPipeline(runID, run.InstanceID, run.Mode, d, hosts, extraAll, installPhases)
		hosts, _ = h.repo.RunHosts(runID)
		if !ok {
			h.skipRemaining(runID, hosts, false, true)
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
		h.markHostsDone(runID, hosts)
		h.finish(runID)
		return
	}

	h.stepRunning(runID, "node")
	ok := h.runPhaseNode(runID, run.InstanceID, run.Mode, d, hosts, extraAll, "node", true)
	h.stepDone(runID, "node", ok)
	hosts, _ = h.repo.RunHosts(runID)
	if !ok {
		h.skipRemaining(runID, hosts, false, true)
		h.finish(runID)
		return
	}

	if op == "scale_out" && hasPhase(run.StackKey, run.Mode, stackkit.PhaseScaleOut) {
		h.stepRunning(runID, "scale_out")
		h.runPhaseScaleOut(runID, run.Mode, d, hosts, existing, extraAll)
		hosts, _ = h.repo.RunHosts(runID)
		h.stepDone(runID, "scale_out", !anyHostFailed(hosts))
		h.finish(runID)
		return
	}
	if stackInstallOp(op) && mode.HasBootstrap {
		h.stepRunning(runID, "bootstrap")
		h.runPhaseBootstrap(runID, run.Mode, d, hosts)
		hosts, _ = h.repo.RunHosts(runID)
		h.stepDone(runID, "bootstrap", !anyHostFailed(hosts))
		h.finish(runID)
		return
	}
	h.markHostsDone(runID, hosts)
	h.finish(runID)
}

// failAll 将全部主机标记失败并终止本次运行。
func (h *stackHandler) failAll(runID int64, hosts []model.StackRunHost, msg string) {
	for i := range hosts {
		hosts[i].Status = "failed"
		hosts[i].Error = msg
		hosts[i].PrereqStatus = "failed"
		hosts[i].NodeStatus = "skipped"
		hosts[i].BootstrapStatus = "skipped"
		_ = h.repo.UpdateHost(&hosts[i])
		h.publishHost(runID, &hosts[i], "node")
	}
	h.finish(runID)
}

// skipRemaining 把未执行的阶段与主机标记为跳过。
func (h *stackHandler) skipRemaining(runID int64, hosts []model.StackRunHost, skipNode, skipBoot bool) {
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
		h.publishHost(runID, &hosts[i], "node")
	}
}

// markHostsDone 按各子阶段状态收敛主机总体状态（收敛结果同步推送 SSE，前端运行抽屉实时可见）。
func (h *stackHandler) markHostsDone(runID int64, hosts []model.StackRunHost) {
	for i := range hosts {
		hosts[i].Status = stackHostOverall(hosts[i])
		_ = h.repo.UpdateHost(&hosts[i])
		h.publishHost(runID, &hosts[i], "node")
	}
}

// stackHostOverall 由各子阶段状态聚合出主机总体状态。
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

// finish 汇总运行结果、收尾实例状态并广播完成事件。
func (h *stackHandler) finish(runID int64) {
	hosts, _ := h.repo.RunHosts(runID)
	h.markHostsDone(runID, hosts)
	status, _ := h.repo.FinishRun(runID)
	// 步骤收敛兜底：正常推进时每步都已落终态；失败中止路径残留的 running/pending 在这里收口
	_ = h.repo.ConvergeRunSteps(runID)
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
