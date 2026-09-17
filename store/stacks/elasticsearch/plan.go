package elasticsearch

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles Elasticsearch 角色规划（docs/新增套件说明.md 第二张表）：
//   cluster       —— 引导机 = 引导主节点（boot_master，与 ClusterExtra 注入的 __roles=master,data 对应），
//                    其余 = 数据节点（data）；
//   cold_warm_hot —— 一机多容器：每台主机按勾选的 roles 逐角色落位（每个勾选格 = 一个独立
//                    ES 容器，master 与协调可同机叠加、数据层 hot/warm/cold 可叠加），与运行期
//                    变量注入（vars.go）、服务登记（register.go）、接入端点（endpoint.go）同源；
//                    未勾选（空 roles）= 自动模式：预览为单条「自动分配（数据节点）」落点，
//                    实际部署由 cwhHostRoles 兜底为冷热温全数据层，手动勾选行标 manual 来源；
//   未知模式      —— 引擎通用兜底（FallbackMasterMember，主/成员两档）。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	switch in.Mode {
	case "cluster":
		in.Roles.AddAt("elasticsearch", "boot_master", "引导主节点（master, data）", "", in.Src(), in.Roles.Boot)
		in.Roles.AddRest("elasticsearch", "data", "数据节点（data）")
	case "cold_warm_hot":
		hasMaster := false
		for _, h := range in.Roles.All {
			rs := cwhRawRunHostRoles(h)
			if len(rs) == 0 {
				// 自动模式：未勾选主机按数据节点部署，预览以单条灰色「自动分配」落点展示
				in.Roles.AddAt("elasticsearch", "auto", "自动分配（数据节点）", "", "auto", h)
				continue
			}
			for _, r := range rs {
				if r == "master" {
					hasMaster = true
				}
				in.Roles.AddAt("elasticsearch", r, cwhRoleLabel(r), "", "manual", h)
			}
		}
		if !hasMaster {
			in.Roles.Warn("尚未指定 master 候选主机（至少 1 台，生产建议 ≥3 台奇数）")
		}
	default:
		in.FallbackMasterMember()
	}
	return plan, nil
}

// cwhRoleLabel 冷热温角色展示名（与前端 esRoleLabel / 端点 tierLabel 同一词表）。
func cwhRoleLabel(role string) string {
	switch role {
	case "master":
		return "master 候选"
	case "coordinator":
		return "纯协调"
	case "data_hot":
		return "数据-hot"
	case "data_warm":
		return "数据-warm"
	case "data_cold":
		return "数据-cold"
	}
	return role
}
