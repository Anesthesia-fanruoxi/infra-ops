package elasticsearch

import (
	"infra-ops/store/stackkit"
)

// endpoint.go：Elasticsearch 接入端点（docs/新增套件说明.md · EndpointProvider）。
//   cluster       —— 单条入口 Elasticsearch（role 落主机角色，与登记台账同名同源）；
//   cold_warm_hot —— 一机多容器：对外仅暴露协调/master 入口（协调优先，其次 master），
//                    component=角色键（探活页签归属）；纯数据主机不给接入端点
//                    （数据节点端口 9220/9230/9240 仅供内部通信，由前端探活合成逐容器检查）；
//   其余（未知模式）—— 单条通用入口，role 落主机角色。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	ip := ctx.Host.HostIP
	if ctx.Mode != "cold_warm_hot" {
		return []stackkit.Endpoint{{
			Name: "Elasticsearch", Component: "elasticsearch",
			URL: "http://" + ip + ":" + stackkit.PickParam(ctx.Params, "port", "9200"), Role: ctx.Host.Role,
		}}
	}
	roles := cwhHostRoles(ctx.Params)
	if stackkit.Contains(roles, "coordinator") {
		return []stackkit.Endpoint{{
			Name: "Elasticsearch-协调", Component: "coordinator",
			URL: "http://" + ip + ":" + cwhRolePort("coordinator"), Role: "coordinator",
		}}
	}
	if stackkit.Contains(roles, "master") {
		return []stackkit.Endpoint{{
			Name: "Elasticsearch-master", Component: "master",
			URL: "http://" + ip + ":" + cwhRolePort("master"), Role: "master",
		}}
	}
	return nil
}
