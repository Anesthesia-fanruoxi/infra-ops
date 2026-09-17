package elasticsearch

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：Elasticsearch 服务登记（docs/新增套件说明.md · ServiceRegistrar）。
//
//	cluster       —— 主 / 工作节点均登记 Elasticsearch 入口（http://<ip>:<port>）；
//	cold_warm_hot —— 一机多容器：数据节点端口仅供内部通信，不对外登记；每台主机仅登记
//	                 协调入口（优先）或 master 入口，纯数据主机不产生接入台账；
//	其余（未知模式）—— 回落引擎通用角色投影。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	ip := ctx.Host.HostIP
	switch ctx.Mode {
	case "cold_warm_hot":
		port, label := "", ""
		for _, r := range cwhHostRoles(ctx.Params) {
			if r == "coordinator" {
				port, label = cwhRolePort(r), "协调"
				break
			}
		}
		if port == "" {
			for _, r := range cwhHostRoles(ctx.Params) {
				if r == "master" {
					port, label = cwhRolePort(r), "master"
					break
				}
			}
		}
		if port != "" {
			ctx.Emit(model.HostService{
				HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "Elasticsearch-" + label,
				URL: "http://" + ip + ":" + port, Web: true,
				TemplateID: 0, InstanceID: ctx.InstanceID,
			})
		}
	case "cluster":
		switch ctx.Host.Role {
		case "master", "worker":
			ctx.Emit(model.HostService{
				HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "Elasticsearch",
				URL: "http://" + ip + ":" + stackkit.PickParam(ctx.Params, "port", "9200"), Web: true,
				TemplateID: 0, InstanceID: ctx.InstanceID,
			})
		default:
			ctx.Emit(stackkit.RoleService(ctx))
		}
	default:
		stackkit.RegisterByRole(ctx)
		return
	}
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
