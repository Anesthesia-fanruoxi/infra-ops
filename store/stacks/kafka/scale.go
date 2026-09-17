package kafka

import (
	"fmt"

	"infra-ops/store/stackkit"
)

// scale.go：Kafka 的缩容保护（套件专属，零通用层分支）。
//
// 为什么 kafka 必须自己拦：KRaft 的 `controller.quorum.voters` 与 ZK 的 `zoo.cfg`
// 里的 `server.N` 都是**部署时按当时的 __node_ips 固化**在每台主机上的
// （见 scripts/kraft-node.sh、scripts/zk-node.sh），缩容只停容器、
// **不会同步收缩仲裁成员列表**。存活成员少于多数派后：
//
//	KRaft：没有 active controller ⇒ 元数据变更全部失败、重启的副本永久选不出主，
//	       且**不会自愈** —— 必须人工改回每台的 server.properties 并重启；
//	ZK   ：ensemble 失去 leader ⇒ broker 超过 zookeeper.session.timeout.ms（默认 18s）
//	       自行退出，整个集群停摆。
//
// 而 kafka 的角色全是 AddAll（全成员）与 AddAt(aux)（附加 UI），
// **没有任何 scope=="" 的落点角色**，引擎的通用落点保护（api/stack 的
// planProtectedHosts）对 kafka 命中为空 —— 所以保护只能由本套件给出。

// ScaleGuard 实现 stackkit.ScaleGuard：两种模式都按「仲裁多数派」判定。
func (d *Driver) ScaleGuard(ctx stackkit.ScaleCtx) error {
	switch ctx.Mode {
	case "kraft":
		return guardQuorum(ctx, "KRaft",
			"失去多数派后 controller 无法选出 leader，元数据变更失败且不会自愈")
	case "zk":
		return guardQuorum(ctx, "ZooKeeper",
			"失去 leader 后 broker 会因会话超时自行退出，集群整体停摆")
	}
	return nil
}

// guardQuorum 按「缩容后剩余主机数必须覆盖多数派」判定。
//
// 多数派按**当前在役主机数**计算（⌊N/2⌋+1），这是一个保守（偏严）的估计：
// 扩容后的集群里 broker-only 成员不是 voter，真实 voters 比 N 小 ⇒ 算出的多数派偏大
// ⇒ 拦截更严。偏差方向安全（宁可多拦，不可放行到失去仲裁），且无需访问主机即可判定。
//
// 两道闸：
//  1. Remain ≥ ⌊N/2⌋+1 —— 硬性多数派；
//  2. Remain ≥ 3 —— 生产下限（2 台时任一节点故障即刻失去仲裁，且 2 不是更好选择）。
func guardQuorum(ctx stackkit.ScaleCtx, label, why string) error {
	active := len(ctx.Active)
	if active == 0 {
		return nil
	}
	if quorum := active/2 + 1; ctx.Remain < quorum {
		return fmt.Errorf("%s 模式缩容后至少保留 %d 台主机（当前 %d 台成员的多数派），本次将剩余 %d 台：%s",
			label, quorum, active, ctx.Remain, why)
	}
	if ctx.Remain < 3 {
		return fmt.Errorf("%s 模式缩容后至少保留 3 台主机（本次将剩余 %d 台）：%s",
			label, ctx.Remain, why)
	}
	return nil
}
