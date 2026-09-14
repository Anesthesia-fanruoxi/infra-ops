package powerjob

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles PowerJob 角色规划（原 api/stack/stack_plan_generic.go 的 powerjob case 原样搬入）：
// 引导节点承载 MySQL（除非 db_host 指定外部库），其余为对等 Server 成员。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	if in.Mode != "cluster" {
		in.FallbackMasterMember()
		return plan, nil
	}
	label := "引导节点（MySQL + Server）"
	if dbHost := in.Get("db_host"); dbHost != "" {
		label = "引导节点"
		in.Roles.Warn("使用外部 MySQL %s，引导节点不再自动部署 MySQL", dbHost)
	}
	in.Roles.AddAt("powerjob", "boot", label, "", in.Src(), in.Roles.Boot)
	in.Roles.AddRest("powerjob", "server", "Server 成员")
	return plan, nil
}
