package redis

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles Redis 角色规划（原 api/stack/stack_plan_generic.go 的三个 redis case 原样搬入）：
//   - replication：引导机 = master，其余 = replica；
//   - sentinel：引导机 = master，其余 = replica，且全员再挂 sentinel；
//   - cluster：全员 node（主从与哈希槽由 CLUSTER CREATE 自动分配，预览不指定具体主从）。
//
// 落点顺序与警告文案与拆分前逐字节一致（被 P0 行为快照冻结）。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	switch in.Mode {
	case "replication":
		in.Roles.AddAt("redis", "master", "Redis Master", "", in.Src(), in.Roles.Boot)
		in.Roles.AddRest("redis", "replica", "Redis Replica")
	case "sentinel":
		in.Roles.AddAt("redis", "master", "Redis Master", "", in.Src(), in.Roles.Boot)
		in.Roles.AddRest("redis", "replica", "Redis Replica")
		in.Roles.AddAll("redis", "sentinel", "Sentinel")
	case "cluster":
		in.Roles.AddAll("redis", "node", "Redis Cluster 节点")
		in.Roles.Warn("主从与哈希槽由 Redis Cluster 自动分配（--cluster-replicas %s），预览不指定具体主从",
			in.FirstNonEmpty(in.Get("replicas"), "0"))
	default:
		in.FallbackMasterMember()
		return plan, nil
	}
	return plan, nil
}
