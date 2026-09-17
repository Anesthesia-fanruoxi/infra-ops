package rabbitmq

import (
	"fmt"
	"sort"
	"strings"

	"infra-ops/store/stackkit"
)

// scale.go：RabbitMQ 的缩容保护与缩容软下线（套件专属，零通用层分支）。
//
// 两项能力的分工（见 store/stackkit/capability.go）：
//   - ScaleGuard     写库前拦截「缩容后失去冗余」的请求（Q1：生产建议级下限 3 台）；
//   - ScaleInDrainer 缩容前，在存活节点上把待移除节点逐个 stop_app → forget_cluster_node
//     （Q2：消除幽灵节点；被移除节点不再以 down 状态驻留集群视图，quorum 队列的
//     副本集合随 forget 一并收缩）。未声明该能力时引擎降级为「直接停服」，
//     被移除节点会长期留在集群视图，voter 数下降后可能低于多数派。

// ScaleGuard 实现 stackkit.ScaleGuard（Q1）。
//
// 成员侧没有硬下限（剩余 ≥1 由通用层拦截、引导机由通用落点保护拦截），
// 这里只补生产建议级下限：低于 3 台时任一节点故障都可能使 quorum 队列失去多数派
// （副本数 2 时多数派即 2，挂一台队列即不可用）。
func (d *Driver) ScaleGuard(ctx stackkit.ScaleCtx) error {
	if ctx.Mode != "cluster" || len(ctx.Active) == 0 {
		return nil
	}
	if ctx.Remain < 3 {
		return fmt.Errorf("RabbitMQ 集群缩容后至少保留 3 台主机（本次将剩余 %d 台）：低于 3 台后 quorum 队列失去多数派冗余，任一节点故障即可能不可用", ctx.Remain)
	}
	return nil
}

// DrainParams 实现 stackkit.ScaleInDrainer（Q2）：cluster 模式缩容前需要软下线。
//
// 给出待移除节点的节点名列表（rabbit@<ip>，与 node.sh 的 RABBITMQ_NODENAME=rabbit@{{__ip}}
// 命名一致）；脚本在存活节点上远程 stop_app 后 forget_cluster_node，
// 主流程随后才停各主机容器。
func (d *Driver) DrainParams(ctx stackkit.DrainCtx) (map[string]string, bool) {
	if ctx.Mode != "cluster" || len(ctx.Removed) == 0 {
		return nil, false
	}
	nodes := make([]string, 0, len(ctx.Removed))
	for i := range ctx.Removed {
		nodes = append(nodes, "rabbit@"+ctx.Removed[i].HostIP)
	}
	sort.Strings(nodes)
	return map[string]string{"remove_nodes": strings.Join(nodes, ",")}, true
}
