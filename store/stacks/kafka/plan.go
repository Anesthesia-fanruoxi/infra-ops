package kafka

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles Kafka 角色规划（原 api/stack/stack_plan_generic.go 的两个 kafka case 原样搬入）：
//   - kraft：全员 broker（内嵌 controller 仲裁）；
//   - zk：全员 ZooKeeper + 全员 broker（每台两容器）；
//   - enable_ui=yes：首台附带 Kafka UI（aux，不参与缩容保护）。
//
// 落点顺序与警告文案与拆分前逐字节一致（被 P0 行为快照冻结）。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	switch in.Mode {
	case "kraft":
		in.Roles.AddAll("kafka", "broker", "Broker + Controller（KRaft 仲裁）")
	case "zk":
		in.Roles.AddAll("kafka", "zk", "ZooKeeper")
		in.Roles.AddAll("kafka", "broker", "Broker")
	default:
		in.FallbackMasterMember()
		return plan, nil
	}
	if in.IsYes("enable_ui") {
		in.Roles.AddAt("kafka", "ui", "Kafka UI", "aux", "auto", in.Hosts[0])
		in.Roles.Warn("Kafka UI 部署在首台 %s", in.Hosts[0].HostIP)
	}
	return plan, nil
}
