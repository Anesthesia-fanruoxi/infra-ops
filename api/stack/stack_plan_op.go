package stack

import (
	"encoding/json"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// planForStackOp 按操作类型产出角色计划（docs/角色物化设计.md §3.1 时机矩阵）：
//   - create / add_component：生成或重规划（rev+1），手动项继承；
//   - scale_out / scale_in：增量刷新——落点角色冻结，全员/成员角色随成员伸缩（拍板 ②）；
//   - reinstall：只读沿用当前 Plan（缺失则惰性生成），保证重装后落点不变。
//
// 全部套件适用：清单式计划套件（大数据底座）走本文件的分支，其余套件走 planForGenericOp。
// 返回 nil 表示该操作不涉及计划。
func (h *stackHandler) planForStackOp(op string, d stackkit.Driver, mode *model.StackMode,
	inst *model.StackInstance, params map[string]string, hosts []model.StackRunHost,
	removeIDs []int64, masterHostID int64) (*model.RolePlan, error) {
	if d == nil {
		return nil, nil
	}
	if !suiteSelfPlans(d.Key()) {
		return h.planForGenericOp(op, d, mode, inst, params, hosts, removeIDs, masterHostID)
	}
	switch op {
	case "create", "add_component":
		userMasters := map[string]string{}
		_ = json.Unmarshal([]byte(params["masters"]), &userMasters)
		manualKeys := make([]string, 0, len(userMasters))
		for k, v := range userMasters {
			if strings.TrimSpace(v) != "" {
				manualKeys = append(manualKeys, k)
			}
		}
		plan, err := planBigdataRoles(hosts, PlanOptions{Op: op, Masters: userMasters, ManualKeys: manualKeys})
		if err != nil {
			return nil, err
		}
		// 重规划：rev 在既有计划上递增（§3.2 只保留最近一份）
		// create 时实例尚未落库（inst == nil），计划从 rev 1 起算，无既有计划可继承
		if inst != nil {
			if old := decodeRolePlan(inst.RolePlanJSON); old != nil && old.Rev > 0 {
				plan.Rev = old.Rev + 1
			}
		}
		return &plan, nil

	case "reinstall":
		if inst == nil {
			return nil, nil
		}
		return h.loadRolePlan(inst)

	case "scale_out":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		all := planFullMemberSet(activeInstanceHosts(inst), hosts)
		applyPlanToHosts(all, old) // 冻结落点：全量投影进成员参数，重算结果与旧 Plan 一致
		plan, perr := planBigdataRoles(all, PlanOptions{Op: "scale_out", Masters: old.Masters, ManualKeys: old.ManualKeys})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil

	case "scale_in":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		removeSet := map[int64]bool{}
		for _, id := range removeIDs {
			removeSet[id] = true
		}
		remaining := make([]model.StackInstanceHost, 0, len(inst.Hosts))
		for _, hh := range activeInstanceHosts(inst) {
			if !removeSet[hh.HostID] {
				remaining = append(remaining, hh)
			}
		}
		all := instanceHostsAsRunHosts(remaining)
		applyPlanToHosts(all, old) // 冻结落点：移除的只是全员角色节点，落点保持不变
		plan, perr := planBigdataRoles(all, PlanOptions{Op: "scale_in", Masters: old.Masters, ManualKeys: old.ManualKeys})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil
	}
	return nil, nil
}

// planForGenericOp 通用套件（非 bigdata）按操作产出角色计划：
//   - create：生成（主/引导节点来自 masterHostID，缺省回落首台，标注来源）；
//   - scale_out：全成员集重算——引导节点由既有成员的 master 角色锚定，新机自动承担 rest 角色；
//   - scale_in：剩余成员重算（引导节点若在移除集内已被前置校验拦截）；
//   - reinstall：只读沿用当前 Plan。
func (h *stackHandler) planForGenericOp(op string, d stackkit.Driver, mode *model.StackMode,
	inst *model.StackInstance, params map[string]string, hosts []model.StackRunHost,
	removeIDs []int64, masterHostID int64) (*model.RolePlan, error) {
	modeKey := ""
	if mode != nil {
		modeKey = mode.Key
	}
	switch op {
	case "create":
		plan, err := planGenericRoles(d, hosts, PlanOptions{
			Op: op, Mode: modeKey, Params: params, ManualMaster: masterHostID > 0,
		})
		if err != nil {
			return nil, err
		}
		// create 时实例尚未落库（inst == nil），计划从 rev 1 起算。此分支只处理 create
		// （通用套件无 add_component 语义），保留判空以便将来接入增量操作时语义不变。
		if inst != nil {
			if old := decodeRolePlan(inst.RolePlanJSON); old != nil && old.Rev > 0 {
				plan.Rev = old.Rev + 1
			}
		}
		return &plan, nil

	case "reinstall":
		if inst == nil {
			return nil, nil
		}
		return h.loadRolePlan(inst)

	case "scale_out":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		all := planFullMemberSet(activeInstanceHosts(inst), hosts)
		plan, perr := planGenericRoles(d, all, PlanOptions{
			Op: op, Mode: inst.Mode, Params: params, ManualMaster: anyMasterRoleHost(all),
		})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil

	case "scale_in":
		if inst == nil {
			return nil, nil
		}
		old, err := h.loadRolePlan(inst)
		if err != nil {
			return nil, err
		}
		removeSet := map[int64]bool{}
		for _, id := range removeIDs {
			removeSet[id] = true
		}
		remaining := make([]model.StackInstanceHost, 0, len(inst.Hosts))
		for _, hh := range activeInstanceHosts(inst) {
			if !removeSet[hh.HostID] {
				remaining = append(remaining, hh)
			}
		}
		all := instanceHostsAsRunHosts(remaining)
		plan, perr := planGenericRoles(d, all, PlanOptions{
			Op: op, Mode: inst.Mode, Params: params, ManualMaster: anyMasterRoleHost(all),
		})
		if perr != nil {
			return nil, perr
		}
		plan.Rev = old.Rev + 1
		return &plan, nil
	}
	return nil, nil
}
