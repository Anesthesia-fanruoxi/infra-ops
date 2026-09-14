package elasticsearch

import (
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：Elasticsearch 服务登记（原 api/stack.registerStackService 的 elasticsearch 分支原样迁入）。
//
//	cluster       —— 主 / 工作节点均登记 Elasticsearch 入口（http://<ip>:<port>）；
//	cold_warm_hot —— 按每台主机勾选的 roles 逐条登记（携带 tier 标签，协议随 SSL 开关 http/https）；
//	其余（未知模式）—— 回落引擎通用角色投影（与拆分前的 default 分支一致）。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	ip := ctx.Host.HostIP
	switch ctx.Mode {
	case "cold_warm_hot":
		scheme := "http"
		if strings.EqualFold(strings.TrimSpace(ctx.Params["ssl_enabled"]), "true") {
			scheme = "https"
		}
		bind := func(label string) {
			ctx.Emit(model.HostService{
				HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "Elasticsearch-" + label,
				URL: scheme + "://" + ip + ":" + stackkit.PickParam(ctx.Params, "port", "9200"), Web: true,
				TemplateID: 0, InstanceID: ctx.InstanceID,
			})
		}
		registered := false
		for _, r := range strings.Split(ctx.Params["roles"], ",") {
			r = strings.TrimSpace(r)
			switch r {
			case "master":
				bind("master")
			case "coordinator":
				bind("协调")
			case "data_hot":
				bind("数据-hot")
			case "data_warm":
				bind("数据-warm")
			case "data_cold":
				bind("数据-cold")
			}
			if r != "" {
				registered = true
			}
		}
		if !registered {
			bind("节点")
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
