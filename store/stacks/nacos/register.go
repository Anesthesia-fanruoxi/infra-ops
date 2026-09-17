package nacos

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：Nacos 服务登记（N3）。
//
// 拆分前的 registry 里没有 nacos 条目（D4）⇒ 服务台账一直给 http://<ip>（无端口、
// web:false），控制台点不进去。这里补回真实入口，与探活端点同源同语义：
//   - 每台登记控制台入口（http://<ip>:8848/nacos/，web:true）；
//   - 首节点（master）且未指定外部 MySQL 时额外登记本机数据库（jdbc，非 web）；
//   - 未知模式回落通用角色投影（与拆分前 default 分支一致）。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	if ctx.Mode != "cluster" {
		stackkit.RegisterByRole(ctx)
		return
	}
	ip := ctx.Host.HostIP
	port := stackkit.PickParam(ctx.Params, "port", "8848")
	ctx.Emit(model.HostService{
		HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "Nacos 控制台",
		URL: "http://" + ip + ":" + port + "/nacos/", Web: true,
		TemplateID: 0, InstanceID: ctx.InstanceID,
	})
	if hasLocalDB(ctx.Host.Role, ctx.Params) {
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "MySQL（nacos 库）",
			URL: "jdbc:mysql://" + ip + ":3306/nacos", Web: false,
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	}
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
