package stack

// 任务 09（套件目录化拆分）P2 迁移进度闸门。
//
// 角色计划「逐字段一致」不足以证明套件分支真的搬走了——引擎 switch 与套件驱动在同一套
// 输入下会给出相同结果。本测试用**结构性断言 + 反证**施加约束：
//
//  1. 已目录化的套件必须实现 stackkit.RolePlanner（引擎走能力断言）；
//  2. planRolesInEngine（引擎残留分支）必须不再认识它们——对已迁移套件只能退化为通用兜底，
//     若哪天有人在引擎里重新加回 `kafka` 分支，本测试立刻失败。

import (
	"testing"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// migratedAtP2 已迁入 store/stacks/<key> 的套件（P2-5 收尾时等于全部 9 个）。
var migratedAtP2 = []string{
	"kafka", "rabbitmq", "rocketmq", "nacos", "powerjob",
	"redis", "elasticsearch", "elfk", "bigdata",
}

// modeAgnosticSuites 原引擎分支「不判 mode」的套件：
//   - elasticsearch / elfk：原 case 只判 key；
//   - bigdata：走独立的清单式 planner（planBigdataRoles），内部无 mode 分支。
//
// 因此它们的角色落点与模式无关，未知模式**不**走兜底（与拆分前逐字节一致），从下述断言中排除。
var modeAgnosticSuites = map[string]bool{"elasticsearch": true, "elfk": true, "bigdata": true}

func TestPlanRolesMigratedSuitesUseDriver(t *testing.T) {
	for _, key := range migratedAtP2 {
		d := store.FindStackDriver(key)
		if d == nil {
			t.Fatalf("套件 %s 未登记", key)
		}
		if _, ok := d.(stackkit.RolePlanner); !ok {
			t.Fatalf("套件 %s 未实现 stackkit.RolePlanner：角色规划仍在引擎 switch 里", key)
		}
	}
}

func TestPlanRolesRemovedFromEngine(t *testing.T) {
	hosts := []model.StackRunHost{
		{HostID: 1, HostName: "n1", HostIP: "10.0.0.1", Seq: 1},
		{HostID: 2, HostName: "n2", HostIP: "10.0.0.2", Seq: 2},
	}
	for _, key := range migratedAtP2 {
		roles := stackkit.NewRoles(hosts)
		in := stackkit.PlanInput{
			Blueprint: model.StackBlueprint{Key: key},
			Mode:      "cluster",
			Hosts:     hosts,
			Roles:     roles,
		}
		planRolesInEngine(in)
		got := roles.PlanHosts(hosts)
		if len(got) != 2 || len(got[0].Roles) != 1 || len(got[1].Roles) != 1 {
			t.Fatalf("套件 %s 仍被引擎分支处理: %+v", key, got)
		}
		if got[0].Roles[0].Role != "master" || got[1].Roles[0].Role != "member" {
			t.Fatalf("套件 %s 仍被引擎分支处理（应为通用兜底 master/member）: %+v", key, got)
		}
	}
}

// 未知模式下套件驱动必须与拆分前引擎 default 分支一致（引导机主节点 + 其余成员），
// 防止目录化改变「模式不认识」这一边界行为。
//
// 例外：elasticsearch / elfk 的原引擎分支只判 key 不判 mode（modeAgnosticSuites），
// 未知模式同样给两档/编排骨架，不兜底——这与拆分前一致，故从本断言中排除。
func TestPlanRolesUnknownModeFallsBack(t *testing.T) {
	hosts := []model.StackRunHost{
		{HostID: 1, HostName: "n1", HostIP: "10.0.0.1", Seq: 1},
		{HostID: 2, HostName: "n2", HostIP: "10.0.0.2", Seq: 2},
	}
	for _, key := range migratedAtP2 {
		if modeAgnosticSuites[key] {
			continue
		}
		bp := store.FindBuiltinStack(key)
		if bp == nil {
			t.Fatalf("套件 %s 未登记", key)
		}
		plan, err := planGenericRoles(bp, hosts, PlanOptions{Op: "create", Mode: "不存在的模式"})
		if err != nil {
			t.Fatalf("套件 %s 未知模式应兜底而非报错: %v", key, err)
		}
		if len(plan.Hosts) != 2 || plan.Hosts[0].Roles[0].Role != "master" || plan.Hosts[1].Roles[0].Role != "member" {
			t.Fatalf("套件 %s 未知模式兜底不一致: %+v", key, plan.Hosts)
		}
	}
}
