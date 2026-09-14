package kafka

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// register.go：Kafka 服务登记（原 api/stack.registerStackService 的 registry["kafka"] 条目与
// kafka/zk 特判原样迁入）。
//
//	运行期角色 node —— 登记 Kafka Broker 入口（kafka://<ip>:<port>）；
//	其他角色       —— 回落引擎通用角色投影（拆分前的 default 分支）；
//	zk 模式        —— 额外登记 ZooKeeper 入口（每台两容器）。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	ip := ctx.Host.HostIP
	if ctx.Host.Role == "node" {
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "Kafka Broker",
			URL: "kafka://" + ip + ":" + stackkit.PickParam(ctx.Params, "port", "9092"), Web: false,
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	} else {
		ctx.Emit(stackkit.RoleService(ctx))
	}
	if ctx.Mode == "zk" {
		ctx.Emit(model.HostService{
			HostID: ctx.Host.HostID, HostIP: ip, ServiceName: "ZooKeeper",
			URL: "zookeeper://" + ip + ":" + stackkit.PickParam(ctx.Params, "zk_port", "2181"), Web: false,
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	}
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}
