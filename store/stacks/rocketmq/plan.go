package rocketmq

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles RocketMQ 角色规划（原 api/stack/stack_plan_generic.go 的 rocketmq case 原样搬入）：
// 首节点（Master）承载 NameServer + Broker Master，其余自动成为同组 Broker Slave。
// 注：NameServer / Broker 的服务登记在 P3（B8）随 ServiceRegistrar 一并迁入本目录。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	if in.Mode != "cluster" {
		in.FallbackMasterMember()
		return plan, nil
	}
	in.Roles.AddAt("rocketmq", "master", "NameServer + Broker Master", "", in.Src(), in.Roles.Boot)
	in.Roles.AddRest("rocketmq", "slave", "Broker Slave")
	return plan, nil
}
