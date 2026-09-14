package stack

import (
	"fmt"
	"sort"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// planGenericRoles 通用套件（非 bigdata）的角色规划（docs/角色物化设计.md §二·全套件覆盖）。
//
// P2 起本函数降为**通用外壳**，与套件无关，只做四件事：
//
//  1. 规范化（按 Seq 升序）与最小主机数校验；
//  2. 装配 stackkit.PlanInput（含落点助手 Roles）；
//  3. 能力断言：套件实现了 stackkit.RolePlanner 就把角色规划整体交给它（引擎零套件分支）；
//  4. 通用收尾：主机行 / 来源标注 / AssignMaster 提示。
//
// 套件分支逐期迁出，B7 后本文件只剩排序、落点与兜底默认（设计文档 §7）。
func planGenericRoles(d stackkit.Driver, hosts []model.StackRunHost, opts PlanOptions) (model.RolePlan, error) {
	plan := model.RolePlan{Op: opts.Op, GeneratedAt: nowLocal(), Rev: 1}
	if d == nil {
		return plan, fmt.Errorf("套件不存在")
	}
	if len(hosts) == 0 {
		return plan, fmt.Errorf("角色规划需要至少一台主机")
	}
	norm := make([]model.StackRunHost, len(hosts))
	copy(norm, hosts)
	sort.SliceStable(norm, func(i, j int) bool { return norm[i].Seq < norm[j].Seq })

	bp := d.Blueprint()
	md := stackkit.ModeDef(bp, opts.Mode)
	if md != nil && len(norm) < md.MinHosts {
		return plan, fmt.Errorf("%s「%s」至少需要 %d 台主机（当前 %d 台）", bp.Name, md.Label, md.MinHosts, len(norm))
	}

	roles := stackkit.NewRoles(norm)
	in := stackkit.PlanInput{
		Blueprint:    bp,
		Op:           opts.Op,
		Mode:         opts.Mode,
		Params:       opts.Params,
		Masters:      opts.Masters,
		ManualKeys:   opts.ManualKeys,
		ManualMaster: opts.ManualMaster,
		Hosts:        norm,
		Roles:        roles,
	}

	if p, ok := d.(stackkit.RolePlanner); ok {
		// 已目录化套件：角色规划完全由套件声明，引擎侧零套件分支。
		p2, err := p.PlanRoles(in)
		if err != nil {
			return p2, err
		}
		plan = p2
		// 自建主机行的套件（bigdata 式清单计划：自算落点、自填 Injected/Masters、自定 warning 顺序）
		// 由驱动全权负责整份计划，引擎不再改写——否则通用收尾会用空落点助手覆盖驱动结果。
		if len(plan.Hosts) > 0 {
			if plan.GeneratedBy == "" {
				plan.GeneratedBy = in.Src()
			}
			return plan, nil
		}
	} else {
		planRolesInEngine(in)
	}

	// 通用收尾（与拆分区前逐字节一致）：
	// 主机行按 Seq 升序展开，无角色的主机给空数组而非 null；
	// 警告 = 套件声明的自动落点提示 + AssignMaster 兜底提示（顺序不变）。
	plan.Hosts = roles.PlanHosts(norm)
	plan.Warnings = append(plan.Warnings, roles.Warnings()...)
	if md != nil && md.AssignMaster && !roles.HasMaster {
		plan.Warnings = append(plan.Warnings, "主节点自动落到 "+roles.Boot.HostIP+"（未显式指定，默认首台）")
	}
	plan.GeneratedBy = in.Src()
	return plan, nil
}

// planRolesInEngine 引擎侧的**通用兜底**落点（引导机主节点 + 其余成员节点）。
//
// B3–B7 把全部 9 个套件的角色规划逐批迁入 store/stacks/<key> 后，本函数已不含任何套件分支；
// 仅用于：① 驱动未实现 RolePlanner 时的降级；② 「未知模式」边界（与拆分前 default 逐字一致）。
func planRolesInEngine(in stackkit.PlanInput) {
	in.FallbackMasterMember()
}
