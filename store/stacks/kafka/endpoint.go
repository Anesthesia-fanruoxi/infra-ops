package kafka

import (
	"infra-ops/store/stackkit"
)

// endpoint.go：Kafka 接入端点。
//
// 未实现 EndpointProvider 时引擎的兜底只有一条 `tcp://<ip>`（名字取实例名、不带端口、
// 不带组件归属），与 register.go 里实际登记的 `kafka://<ip>:<port>` / `zookeeper://<ip>:<port>`
// 并不一致，探活对话框也只能给一个平级色系。这里补齐：
//
//	Kafka Broker  kafka://<ip>:<port>        两模式都有（component=kafka）
//	ZooKeeper     zookeeper://<ip>:<zk_port> 仅 zk 模式（component=zookeeper）
//	KafkaUI       http://<ip>:<ui_port>       仅首台且 enable_ui 打开（component=ui）
//
// component 决定探活对话框的页签归属（骨架按端点的 component 过滤出真实存在的页签）。
func (d *Driver) Endpoints(ctx stackkit.EndpointCtx) []stackkit.Endpoint {
	h := ctx.Host
	port := stackkit.PickParam(ctx.Params, "port", "9092")
	out := []stackkit.Endpoint{{
		Name: "Kafka Broker", Component: compKafka,
		URL: "kafka://" + h.HostIP + ":" + port, Role: h.Role,
	}}
	if ctx.Mode == "zk" {
		zp := stackkit.PickParam(ctx.Params, "zk_port", "2181")
		out = append(out, stackkit.Endpoint{
			Name: "ZooKeeper", Component: compZK,
			URL: "zookeeper://" + h.HostIP + ":" + zp, Role: h.Role,
		})
	}
	// KafkaUI 只部署在首台（PlanRoles 的 AddAt(..., in.Hosts[0])，脚本亦以 SELF_ID=1 为界），
	// 因此只给首台一条 web 端点 —— 缩容移除首台后该端点消失，与「脚本不会在新首台重部署 UI」一致。
	if h.Seq == 1 && stackkit.IsYes(ctx.Params["enable_ui"]) {
		up := stackkit.PickParam(ctx.Params, "ui_port", "8080")
		out = append(out, stackkit.Endpoint{
			Name: "KafkaUI", Component: compUI,
			URL: "http://" + h.HostIP + ":" + up, Role: "ui",
		})
	}
	return out
}
