package rabbitmq

import (
	"infra-ops/store/stackkit"
)

// endpoint.go：RabbitMQ 接入端点（Q4）——
//   cluster —— 每台两条：AMQP 客户端入口（amqp://<ip>:5672）+ 管理台（http://<ip>:15672，web）；
//              管理台 URL 与 register.go 的服务登记同源（默认账号在部署参数里）。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	ip := ctx.Host.HostIP
	return []stackkit.Endpoint{
		{
			Name: "RabbitMQ AMQP", Component: "rabbitmq",
			URL: "amqp://" + ip + ":" + stackkit.PickParam(ctx.Params, "amqp_port", "5672"),
			Role: ctx.Host.Role,
		},
		{
			Name: "RabbitMQ 管理台", Component: "rabbitmq",
			URL: "http://" + ip + ":" + stackkit.PickParam(ctx.Params, "mgmt_port", "15672"),
			Role: ctx.Host.Role,
		},
	}
}
