package rocketmq

import (
	"regexp"
	"strconv"

	"infra-ops/store/stackkit"
)

// summary.go：探活汇总（Summary）与跨主机聚合结论 —— ProbePlugin 的汇总面。
//
// 聚合素材是各主机的「注册」检查项明细（probe.go 产出的 BID 标记）：
//   - NameServer 单点是本套件的架构事实（仅 master 机部署），必须显式说明——
//     它同时是最隐蔽的单点风险（master 故障期间新客户端拉不到路由）；
//   - 副本结论：组内 slave 数 = 副本数。非 DLedger 无自动切换，缩到无 slave
//     （即 M4 ScaleGuard 保护的形态）时探活是唯一的事后观测入口。
//
// 注意：notes 文案不得包含「异常 / 未就绪 / 不符」——引擎按这三个词强制判失败
// （api/stack/stack_verify.go 的 assembleStackVerify），正常汇总必须保持中性。

// rmqBidRE 从「注册」检查项明细里取 BID（Summary 聚合用）。
var rmqBidRE = regexp.MustCompile(`BID=(\d+)`)

// Summary 汇总探活提示与客户端接入入口（替代引擎里的套件 switch）。
func (d *Driver) Summary(ctx stackkit.ProbeCtx, hosts []stackkit.ProbeHostRow) ([]string, string) {
	nsPort := stackkit.PickParam(ctx.Params, "ns_port", "9876")
	brokerPort := stackkit.PickParam(ctx.Params, "broker_port", "10911")
	masterIP := ""
	for _, h := range hosts {
		if h.Role == "master" && masterIP == "" {
			masterIP = h.HostIP
		}
	}
	if masterIP == "" && len(hosts) > 0 {
		masterIP = hosts[0].HostIP
	}
	notes := []string{
		"客户端先连 NameServer（" + masterIP + ":" + nsPort + "）拉取路由，再与 Broker（" + brokerPort + "）通信；生产者/消费者只需配置 NameServer 地址。",
		"NameServer 仅部署在 master 节点（集群唯一入口，单点）：该节点故障期间新客户端无法拉取路由，生产建议以其可用性为最高优先级运维。",
	}
	notes = append(notes, rmqReplicaNotes(hosts)...)
	notes = append(notes, "缩容请走平台流程：缩到只剩 1 台时会被拦截（Broker 无副本且不会自动切换）。")
	return notes, "rocketmq://" + masterIP + ":" + nsPort
}

// rmqReplicaNotes 副本结论（多机聚合）：
// BID=0 计入 master，BID>0 计入 slave；组内 0 个 slave = 无副本形态。
func rmqReplicaNotes(hosts []stackkit.ProbeHostRow) []string {
	masters, slaves := 0, 0
	for _, h := range hosts {
		for _, c := range h.Checks {
			if c.Name != "注册" {
				continue
			}
			if m := rmqBidRE.FindStringSubmatch(c.Detail); m != nil {
				if bid, err := strconv.Atoi(m[1]); err == nil {
					if bid == 0 {
						masters++
					} else {
						slaves++
					}
				}
			}
		}
	}
	if masters == 0 {
		return nil
	}
	if slaves == 0 {
		return []string{"Broker 组内未发现 Slave（无副本）：master 故障将丢失未同步消息且不会自动切换（非 DLedger），生产建议至少 2 台。"}
	}
	return []string{"Broker 组内 " + strconv.Itoa(slaves) + " 个 Slave（异步复制）：master 故障需人工切换，异步复制下的少量未同步消息会丢失。"}
}
