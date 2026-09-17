package nacos

import (
	"strings"

	"infra-ops/store/stackkit"
)

// endpoint.go：Nacos 接入端点（docs/单模式套件方案.md · N3）。
//
//	cluster —— 每台一条控制台入口（http://<ip>:8848/nacos/，web）；
//	           首节点（master）且未指定外部 MySQL 时额外给出本机 MySQL 端点
//	           （jdbc:mysql://<ip>:3306/nacos）——与 node.sh 的首节点条件服务一一对应。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	ip := ctx.Host.HostIP
	port := stackkit.PickParam(ctx.Params, "port", "8848")
	out := []stackkit.Endpoint{{
		Name: "Nacos 控制台", Component: compNacos,
		URL: "http://" + ip + ":" + port + "/nacos/", Role: ctx.Host.Role,
	}}
	if hasLocalDB(ctx.Host.Role, ctx.Params) {
		out = append(out, stackkit.Endpoint{
			Name: "MySQL（nacos 库）", Component: compMySQL,
			URL: "jdbc:mysql://" + ip + ":3306/nacos", Role: "db",
		})
	}
	return out
}

// hasLocalDB 首节点本机部署 MySQL 的判定：运行期角色 master 且未指定外部库。
// 探活端点与「服务登记」共用同一判定，保证两处对「本机是否有 MySQL」的答案一致。
func hasLocalDB(role string, params map[string]string) bool {
	return role == "master" && strings.TrimSpace(params["db_host"]) == ""
}
