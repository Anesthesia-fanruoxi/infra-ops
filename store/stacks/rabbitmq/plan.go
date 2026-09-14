package rabbitmq

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles RabbitMQ 角色规划（原 api/stack/stack_plan_generic.go 的 rabbitmq case 原样搬入）：
// 初始化节点（seed）承载集群初始化，其余成员自动 join。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	if in.Mode != "cluster" {
		in.FallbackMasterMember()
		return plan, nil
	}
	in.Roles.AddAt("rabbitmq", "seed", "初始化节点", "", in.Src(), in.Roles.Boot)
	in.Roles.AddRest("rabbitmq", "member", "集群成员")
	return plan, nil
}
