package powerjob

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：PowerJob 服务登记（P3）——与 endpoint.go 同源同语义。
//
// 为什么在 EndpointProvider 之外另立 ServiceRegistrar（与 nacos/rocketmq 同理）：
// 探活端点（endpoints 段）与部署服务台账（services 段）是两条独立数据面，
// 只修 Endpoints 时 D4 证据链里的 services 仍为通用默认（http://<ip>，
// 无端口、web:false），控制台在平台里点不进去。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	if ctx.Mode != "cluster" {
		stackkit.RegisterByRole(ctx)
		return
	}
	ip := ctx.Host.HostIP
	port := stackkit.PickParam(ctx.Params, "server_port", "7700")
	ctx.Emit(model.HostService{
		HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "PowerJob 控制台",
		URL: "http://" + ip + ":" + port + "/", Web: true,
		TemplateID: 0, InstanceID: ctx.InstanceID,
	})
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
