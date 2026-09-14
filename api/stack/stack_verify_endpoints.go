// 套件探活接入地址：套件实现 EndpointProvider 即由其给出入口（含 ES 冷热温分层 / 大数据 HA 矩阵），
// 未实现即回落通用兜底端点（tcp://<ip>）。
package stack

import (
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

func stackVerifyEndpoints(inst *model.StackInstance, params map[string]string, _ []stackVerifyHost) []stackVerifyEndpoint {
	out := []stackVerifyEndpoint{}
	comps := parseComponentsCSV(params["components"])
	var provider stackkit.EndpointProvider
	if p, ok := store.FindStackDriver(inst.StackKey).(stackkit.EndpointProvider); ok {
		provider = p
	}
	for _, h := range activeInstanceHosts(inst) {
		hp := mergeParamMaps(params, parseJSONMap(h.ParamsJSON))
		overrideBigdataMasters(inst, hp)
		if provider == nil {
			out = append(out, stackVerifyEndpoint{Name: inst.StackName, URL: "tcp://" + h.HostIP, Role: h.Role})
			continue
		}
		for _, ep := range provider.Endpoints(stackkit.EndpointCtx{
			Mode: inst.Mode, Host: h, Params: hp, InstName: inst.StackName, Components: comps,
		}) {
			out = append(out, stackVerifyEndpoint{Name: ep.Name, Component: ep.Component, URL: ep.URL, Role: ep.Role})
		}
	}
	return out
}
