package stack

import (
	"fmt"
	"strings"

	"infra-ops/api/deploy"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// runScaleInDrain 缩容软下线：先向套件询问「本模式是否需要软下线、以及脚本渲染参数」，
// 需要则在存活 leader 上执行 scale_in 阶段脚本，集群回绿后再放行停容器。
//
// 返回 false 表示软下线失败（中止缩容，保护分片数据）；套件未声明能力或不需要软下线时返回 true
// （走通用直接停服路径）。
//
// 职责边界：套件只按待移除主机给出模式相关参数（待排空节点名）；leader 选取、self_ip 注入、
// 脚本加载/渲染/SSH 执行留在引擎。
func (h *stackHandler) runScaleInDrain(runID int64, run *model.StackRun, removed []model.StackRunHost) bool {
	if run == nil || len(removed) == 0 {
		return true
	}
	d := store.FindBuiltinStack(run.StackKey)
	if d == nil {
		return true
	}
	drainer, ok := d.(stackkit.ScaleInDrainer)
	if !ok {
		return true
	}
	drainParams, need := drainer.DrainParams(stackkit.DrainCtx{Mode: run.Mode, Removed: removed})
	if !need {
		return true
	}
	script, err := store.LoadStackPhase(d, run.Mode, stackkit.PhaseScaleIn)
	if err != nil {
		h.appendLog(runID, "node", 0, "", "缩容下线脚本缺失，降级为直接停服: "+err.Error())
		return true
	}

	// 选存活 leader：master 优先，否则首个存活成员；全被移除则无存活，跳过软下线
	removedIDs := map[int64]bool{}
	for i := range removed {
		removedIDs[removed[i].HostID] = true
	}
	var survivors []model.StackRunHost
	if run.InstanceID > 0 {
		if instHosts, ierr := h.repo.InstanceHosts(run.InstanceID, true); ierr == nil {
			for _, ih := range instHosts {
				if removedIDs[ih.HostID] {
					continue
				}
				rh := model.StackRunHost{HostID: ih.HostID, HostName: ih.HostName, HostIP: ih.HostIP, Seq: ih.Seq, ParamsJSON: ih.ParamsJSON}
				p := parseJSONMap(ih.ParamsJSON)
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

	params := parseJSONMap(leader.ParamsJSON)
	params["self_ip"] = leader.HostIP
	for k, v := range drainParams {
		params[k] = v
	}
	varsJSON := stackVarsJSON(d.Blueprint(), run.Mode)
	rendered, rerr := deploy.RenderScript(script, varsJSON, privatizeImages(params))
	if rerr != nil {
		h.appendLog(runID, "node", leader.HostID, leader.HostIP, "缩容下线脚本渲染失败: "+rerr.Error())
		return false
	}
	h.appendLog(runID, "node", leader.HostID, leader.HostIP,
		fmt.Sprintf("软下线开始：在 %s 排空节点 %s", leader.HostIP, params["remove_nodes"]))
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
