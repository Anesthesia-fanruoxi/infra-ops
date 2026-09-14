package elfk

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：ELFK 服务登记（原 api/stack.registerStackService 的 elfk 分支原样迁入）。
// 按组件编排登记：ES 节点全员、Kibana 随引导机、Logstash 落点机、Filebeat 全员。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	ip := ctx.Host.HostIP
	up := func(name, url string, web bool) {
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: name,
			URL: url, Web: web, TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	}
	up("Elasticsearch", "http://"+ip+":"+stackkit.PickParam(ctx.Params, "port", "9200"), true)
	// 引导机额外登记 Kibana（Kibana 固定随引导机；引导机 = Role master 或 Seq 1）
	if ctx.Host.Role == "master" || ctx.Host.Seq == 1 {
		up("Kibana", "http://"+ip+":"+stackkit.PickParam(ctx.Params, "kibana_port", "5601"), true)
	}
	lsHost := stackkit.PickParam(ctx.Params, "logstash_host", "")
	if ctx.Host.Seq == 2 || (lsHost != "" && lsHost == ip) {
		up("Logstash", "http://"+ip+":"+stackkit.PickParam(ctx.Params, "logstash_beats_port", "5044"), false)
	}
	up("Filebeat", "filebeat://"+ip, false)
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
