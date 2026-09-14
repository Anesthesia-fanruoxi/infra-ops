package rabbitmq

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：RabbitMQ 服务登记（原 api/stack.registerStackService 的 registry["rabbitmq/cluster"]
// 条目原样迁入）：主 / 成员节点均登记管理台入口（http://<ip>:<mgmt_port>）；其他角色回落通用投影。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	switch ctx.Host.Role {
	case "master", "worker":
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ctx.Host.HostIP, ServiceName: "RabbitMQ",
			URL: "http://" + ctx.Host.HostIP + ":" + stackkit.PickParam(ctx.Params, "mgmt_port", "15672"), Web: true,
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	default:
		ctx.Emit(stackkit.RoleService(ctx))
	}
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
