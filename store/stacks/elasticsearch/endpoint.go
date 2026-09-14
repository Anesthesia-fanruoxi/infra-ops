package elasticsearch

import (
	"strings"

	"infra-ops/store/stackkit"
)

// endpoint.go：Elasticsearch 接入端点（原 api/stack.stackVerifyEndpoints 的 elasticsearch 分支原样迁入）。
// 集群与冷热温共用：按主机级 roles 逐条给出入口（协议随 SSL 开关），无 roles 时给一条兜底入口。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	h := ctx.Host
	p := stackkit.PickParam(ctx.Params, "port", "9200")
	roles := strings.Split(stackkit.PickParam(ctx.Params, "roles", "coordinator"), ",")
	scheme := "http"
	if strings.EqualFold(strings.TrimSpace(stackkit.PickParam(ctx.Params, "ssl_enabled", "false")), "true") {
		scheme = "https"
	}
	var out []stackkit.Endpoint
	for _, r := range roles {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out = append(out, stackkit.Endpoint{
			Name: "Elasticsearch-" + tierLabel(r), URL: scheme + "://" + h.HostIP + ":" + p, Role: r,
		})
	}
	if len(roles) == 0 {
		out = append(out, stackkit.Endpoint{Name: "Elasticsearch", URL: scheme + "://" + h.HostIP + ":" + p, Role: h.Role})
	}
	return out
}

// tierLabel 将 ES 冷热温角色映射为面向用户的接入标签（原 api/stack.esTierLabel）。
func tierLabel(role string) string {
	switch role {
	case "master":
		return "master"
	case "coordinator":
		return "协调"
	case "data_hot":
		return "数据-hot"
	case "data_warm":
		return "数据-warm"
	case "data_cold":
		return "数据-cold"
	}
	return role
}
