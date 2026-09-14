package elfk

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// PlanRoles ELFK 角色规划（原 api/stack/stack_plan_generic.go 的 elfk case 原样搬入）。
//
// 组件编排型（非主从型）：ES cluster + Kibana 随引导机 + Logstash 单点 + Filebeat 全员。
// 与原 case 一致，本函数只判 key 不判模式（未知模式同样按标准编排落点）。
//
// Logstash 落点三处一致：planner 的 in.Hosts[1] / node.sh 的 node_ips 第 2 段 /
// register 的 Seq==2，且 logstash_host 参数可覆盖（覆盖时为 manual）。
func (d *Driver) PlanRoles(in stackkit.PlanInput) (model.RolePlan, error) {
	plan := in.NewPlan()
	in.Roles.AddAt("elfk", "es_boot", "ES 引导主节点（master, data）", "", in.Src(), in.Roles.Boot)
	in.Roles.AddAt("elfk", "kibana", "Kibana（可视化）", "aux", "auto", in.Roles.Boot)
	in.Roles.AddRest("elfk", "es_data", "ES 数据节点（data）")
	// Logstash 落点：logstash_host 参数显式指定优先，否则第 2 台（Seq 顺序，
	// 与脚本 resolve_logstash_host 的 node_ips 第二段规则逐字节一致）
	lsHost := in.Get("logstash_host")
	lsIdx := -1
	lsSrc := "auto"
	if lsHost != "" {
		for i, h := range in.Hosts {
			if h.HostIP == lsHost {
				lsIdx, lsSrc = i, "manual"
				break
			}
		}
		if lsIdx < 0 {
			in.Roles.Warn("logstash_host %s 不在本次成员列表内，将自动落到第 2 台", lsHost)
		}
	}
	if lsIdx < 0 && len(in.Hosts) > 1 {
		lsIdx = 1
	}
	if lsIdx < 0 && len(in.Hosts) > 0 {
		lsIdx = 0
	}
	if lsIdx >= 0 {
		in.Roles.AddAt("elfk", "logstash", "Logstash（日志管道）", "aux", lsSrc, in.Hosts[lsIdx])
		if lsSrc == "auto" {
			in.Roles.Warn("Logstash 自动落点 %s（可用参数 logstash_host 覆盖）", in.Hosts[lsIdx].HostIP)
		}
	}
	in.Roles.AddAll("elfk", "filebeat", "Filebeat 采集代理")
	return plan, nil
}
