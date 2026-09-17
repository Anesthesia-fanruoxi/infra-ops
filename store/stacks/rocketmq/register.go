package rocketmq

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：RocketMQ 服务登记（M2/D3 补迁，plan.go 注释所欠的 P3/B8）——
// 拆分期前 registry 的 rocketmq/namesrv 与 rocketmq/broker 两条目在「一步成型」
// 合并为 cluster 时丢失，服务台账长期回落到通用投影（裸 http://<ip>，无端口）。
//
// 为什么需要在 EndpointProvider 之外另立 ServiceRegistrar（超出 M2 清单的一处）：
// 探活端点（endpoints 段）与部署服务台账（services 段）是两条独立数据面，
// 只修 Endpoints 时 D3 证据链里的 services 仍为错误 URL；
// 恢复 split 前的登记语义需两者同时落。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	ip := ctx.Host.HostIP
	switch ctx.Host.Role {
	case "master":
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "RocketMQ NameServer",
			URL: "rocketmq://" + ip + ":" + stackkit.PickParam(ctx.Params, "ns_port", "9876"),
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "RocketMQ Broker 主",
			URL: "rocketmq://" + ip + ":" + stackkit.PickParam(ctx.Params, "broker_port", "10911"),
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	case "worker":
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "RocketMQ Broker 从",
			URL: "rocketmq://" + ip + ":" + stackkit.PickParam(ctx.Params, "broker_port", "10911"),
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	default:
		ctx.Emit(stackkit.RoleService(ctx))
	}
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
