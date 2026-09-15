package stack

import (
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// 运行步骤：在 execute 入口一次性物化本次运行的阶段序列（stack_run_steps），
// 引擎推进状态（running→success/failed，失败中止时剩余 pending 由 finish 收敛为 skipped），
// SSE 实时推送，前端按 seq 渲染步骤条。通用骨架只认 key/status，展示可被套件覆盖。

// buildRunSteps 按运行类型构造步骤清单：
//   - 移除类操作（scale_in/uninstall/remove_component）：单步；
//   - 流水线套件（如大数据底座）：Docker 前置 + 蓝图流水线逐阶段（Target=leader 归 bootstrap 日志阶段）；
//   - 通用套件：Docker 前置 + 节点部署（+ 集群初始化），步骤归属全部组件（前端主机视图芯片状态来源）。
func buildRunSteps(op string, d stackkit.Driver, mode *model.StackMode, installPhases []model.StackPhase, allComps string) []model.StackRunStep {
	steps := []model.StackRunStep{}
	seq := 0
	add := func(key, label, target, component, phase string) {
		seq++
		steps = append(steps, model.StackRunStep{Seq: seq, Key: key, Label: label, Target: target, Component: component, Phase: phase})
	}
	switch op {
	case "scale_in":
		add("remove", "缩容下线", "all", "", "node")
		return steps
	case "uninstall":
		add("remove", "卸载集群", "all", "", "node")
		return steps
	case "remove_component":
		add("remove", "卸载组件", "all", "", "node")
		return steps
	}
	if d.Blueprint().RequiresDocker {
		add("prereq", "Docker 环境", "all", "", "prereq")
	}
	if len(installPhases) > 0 {
		for _, ph := range installPhases {
			phase := "node"
			if ph.Target == "leader" {
				phase = "bootstrap"
			}
			add(ph.Key, ph.Label, ph.Target, ph.Component, phase)
		}
		return steps
	}
	add("node", "节点部署", "all", allComps, "node")
	if stackInstallOp(op) && mode != nil && mode.HasBootstrap {
		add("bootstrap", "集群初始化", "leader", allComps, "bootstrap")
	}
	return steps
}

// runStepComponents 通用套件步骤的归属组件：从实例角色计划提取去重后的 comp 列表
// （如 redis 哨兵 = "redis,sentinel"）。计划不可得时返回空（前端芯片仅不显示状态，不影响其它功能）。
func (h *stackHandler) runStepComponents(run *model.StackRun) string {
	if run == nil || run.InstanceID <= 0 {
		return ""
	}
	inst, err := h.repo.GetInstanceFull(run.InstanceID)
	if err != nil || inst == nil {
		return ""
	}
	plan, perr := h.loadRolePlan(inst)
	if perr != nil {
		return ""
	}
	seen := map[string]bool{}
	var comps []string
	for _, ph := range plan.Hosts {
		for _, r := range ph.Roles {
			if r.Comp != "" && !seen[r.Comp] {
				seen[r.Comp] = true
				comps = append(comps, r.Comp)
			}
		}
	}
	return strings.Join(comps, ",")
}

// anyHostFailed 判断主机集中是否存在失败（移除路径的步骤成败依据）。
func anyHostFailed(hosts []model.StackRunHost) bool {
	for i := range hosts {
		if hosts[i].Status == "failed" {
			return true
		}
	}
	return false
}
