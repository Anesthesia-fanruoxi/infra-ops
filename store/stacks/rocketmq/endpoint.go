package rocketmq

import (
	"infra-ops/store/stackkit"
)

// endpoint.go：RocketMQ 接入端点（M2）——补回 D3 丢失的语义（拆分期前 registry 的
// rocketmq/namesrv 与 rocketmq/broker 两条登记在「一步成型」合并时丢失，
// 探活端点长期回落到通用兜底 tcp://<ip>）：
//   - master 机：NameServer（rocketmq://<ip>:9876，仅首台部署）+ Broker（rocketmq://<ip>:10911）；
//   - slave  机：Broker（rocketmq://<ip>:10911）。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	ip := ctx.Host.HostIP
	eps := []stackkit.Endpoint{}
	if ctx.Host.Role == "master" {
		eps = append(eps, stackkit.Endpoint{
			Name: "RocketMQ NameServer", Component: "namesrv",
			URL: "rocketmq://" + ip + ":" + stackkit.PickParam(ctx.Params, "ns_port", "9876"),
			Role: ctx.Host.Role,
		})
	}
	eps = append(eps, stackkit.Endpoint{
		Name: "RocketMQ Broker", Component: "broker",
		URL: "rocketmq://" + ip + ":" + stackkit.PickParam(ctx.Params, "broker_port", "10911"),
		Role: ctx.Host.Role,
	})
	return eps
}
