package powerjob

import (
	"infra-ops/store/stackkit"
)

// endpoint.go：PowerJob 接入端点（docs/单模式套件方案.md · P3）——补回 D4 丢失的语义
// （拆分期前 registry 从无 powerjob 条目，探活端点长期回落到通用兜底 tcp://<ip>，
// 控制台只能手工拼 URL；§5 D4 记载的默认账号 powerjob/powerjob123456）：
//
//	cluster —— 每台一条控制台入口（http://<ip>:7700/，web:true）。
//	           对等集群，worker 与运维人员连任一存活节点均可。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	ip := ctx.Host.HostIP
	port := stackkit.PickParam(ctx.Params, "server_port", "7700")
	return []stackkit.Endpoint{{
		Name: "PowerJob 控制台", Component: compPowerjob,
		URL: "http://" + ip + ":" + port + "/", Role: ctx.Host.Role,
	}}
}
