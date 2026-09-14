package elasticsearch

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles Elasticsearch 角色规划（原 api/stack/stack_plan_generic.go 的 elasticsearch case 原样搬入）：
// 引导机 = 引导主节点（master, data），其余 = 数据节点（data）。
//
// 注 1：原 case 只判 key 不判 mode，故集群与冷热温（含未知模式）共用同一两档骨架——每台主机真正的
// 分层角色（master / coordinator / data_hot|warm|cold）来自主机级参数 roles，由运行期变量注入与
// 脚本消费（B9 VarInjector / stack_vars_cwh.go），角色计划本身不展开分层。
// 注 2：为与拆分前逐字节一致，本函数**不做模式校验**（未知模式同样给两档骨架，而非兜底）。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	in.Roles.AddAt("elasticsearch", "boot_master", "引导主节点（master, data）", "", in.Src(), in.Roles.Boot)
	in.Roles.AddRest("elasticsearch", "data", "数据节点（data）")
	return plan, nil
}
