package redis

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles Redis 角色规划（原 api/stack/stack_plan_generic.go 的三个 redis case 原样搬入）：
//   - replication：引导机 = master，其余 = replica；
//   - sentinel：引导机 = master，其余 = replica，且全员再挂 sentinel；
//     sentinel 是独立服务（独立容器/端口 26379），comp 用 "sentinel" 与 redis 本体区分，
//     运行抽屉主机视图才能各出一枚芯片（2026-09-14 用户反馈哨兵展示不正确后调整）；
//   - cluster：全员 node（主从与哈希槽由 CLUSTER CREATE 自动分配，预览不指定具体主从）。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	switch in.Mode {
	case "replication":
		in.Roles.AddAt("redis", "master", "Redis Master", "", in.Src(), in.Roles.Boot)
		in.Roles.AddRest("redis", "replica", "Redis Replica")
	case "sentinel":
		in.Roles.AddAt("redis", "master", "Redis Master", "", in.Src(), in.Roles.Boot)
		in.Roles.AddRest("redis", "replica", "Redis Replica")
		in.Roles.AddAll("sentinel", "sentinel", "Sentinel")
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
