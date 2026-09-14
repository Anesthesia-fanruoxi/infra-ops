package stack

import (
	"infra-ops/model"
	"infra-ops/store/stacks/bigdata"
)

// 本文件实现「部署前角色物化（RolePlan）」的输入选项与 bigdata 规划转发：
// docs/角色物化设计.md。planner 以 bigdata.ResolveRole 为唯一算法（一行不改），
// 只把调用时机前移到部署前，结果物化为 RolePlan 落库；部署 / 探活 / 扩缩容 / 重装共读同一份计划。
//
// P2-5 起算法实现已迁入 store/stacks/bigdata（plan.go / plan_ha.go / components.go）。

// PlanOptions planner 输入选项。
type PlanOptions struct {
	Op           string            // create / add_component / scale_out / scale_in / reinstall / replan
	Mode         string            // 套件模式 key（通用套件角色目录按 stack+mode 取角色语义）
	Params       map[string]string // 套件参数（enable_ui / db_host / replicas 等影响角色展开）
	Masters      map[string]string // bigdata 权威落点输入（key ∈ bigdata 角色键集合）；缺省键自动落位
	ManualKeys   []string          // 用户显式指定的 masters 键（用于来源标注，跨重规划继承）
	ManualMaster bool              // 通用套件：用户显式指定了主/引导节点（来源标注 manual）
}

// planBigdataRoles 以 bigdata 套件驱动为唯一算法，把权威落点展开为可持久化 RolePlan。
// hosts 需为本次操作的全部成员（含既有成员）；hosts[0].ParamsJSON 须携带 ha/components/masters。
func planBigdataRoles(hosts []model.StackRunHost, opts PlanOptions) (model.RolePlan, error) {
	return bigdata.Plan(hosts, opts.Op, opts.Masters, opts.ManualKeys)
}
