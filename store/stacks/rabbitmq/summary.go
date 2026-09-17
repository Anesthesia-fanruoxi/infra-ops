package rabbitmq

import (
	"regexp"
	"strconv"

	"infra-ops/store/stackkit"
)

// summary.go：探活汇总（Summary）与跨主机聚合结论 —— ProbePlugin 的汇总面。
//
// 聚合素材是各主机的「队列」「集群」检查项明细（probe.go 产出的结构化标记），
// 多机视角下取最危险者：缩容/宕机后「集群在跑、quorum 队列已失去多数派」是最隐蔽的
// 故障形态，本文件是指出它的唯一入口（同 kafka 的仲裁结论）。
//
// 注意：notes 文案不得包含「异常 / 未就绪 / 不符」——引擎按这三个词强制判失败
// （api/stack/stack_verify.go 的 assembleStackVerify），正常汇总必须保持中性。

// 检查项 detail 的结构化标记（probe.go 产出，Summary 还原结论用）。
var (
	rbTotalRE  = regexp.MustCompile(`共 (\d+)`)
	rbReplRE   = regexp.MustCompile(`副本队列 (\d+)`)
	rbOnlineRE = regexp.MustCompile(`最少在线 (\d+)/(\d+)`)
	rbGhostRE  = regexp.MustCompile(`视图外 (\d+)`)
	rbPartRE   = regexp.MustCompile(`分区告警=(\d+)`)
)

// Summary 汇总探活提示与客户端接入入口（替代引擎里的套件 switch）。
func (d *Driver) Summary(ctx stackkit.ProbeCtx, hosts []stackkit.ProbeHostRow) ([]string, string) {
	port := stackkit.PickParam(ctx.Params, "amqp_port", "5672")
	mgmt := stackkit.PickParam(ctx.Params, "mgmt_port", "15672")
	ip := ""
	for _, h := range hosts {
		if h.OK && ip == "" {
			ip = h.HostIP
		}
	}
	if ip == "" && len(hosts) > 0 {
		ip = hosts[0].HostIP
	}
	notes := []string{"客户端连任一存活节点的 AMQP " + port + " 端口即可；管理台 " + mgmt + "。"}
	notes = append(notes, rbQueueNotes(hosts)...)
	notes = append(notes, rbPartitionNotes(hosts)...)
	notes = append(notes, "缩容请走平台流程：会自动软下线（stop_app → forget_cluster_node），被移除节点不会残留为幽灵节点。")
	return notes, "http://" + ip + ":" + mgmt
}

// rbQueueNotes 队列副本结论（多机聚合取最危险者）：
// quorum/stream 队列在线副本低于多数派是「集群在跑、队列已不可用」的隐蔽故障；
// 无副本队列（classic）则是单点故障面，一并提示。
func rbQueueNotes(hosts []stackkit.ProbeHostRow) []string {
	total, repl, online, voters, ghost := -1, 0, -1, -1, 0
	for _, h := range hosts {
		for _, c := range h.Checks {
			if c.Name != "队列" {
				continue
			}
			if m := rbTotalRE.FindStringSubmatch(c.Detail); m != nil && total < 0 {
				total, _ = strconv.Atoi(m[1])
			}
			if m := rbReplRE.FindStringSubmatch(c.Detail); m != nil {
				if n, _ := strconv.Atoi(m[1]); n > repl {
					repl = n
				}
			}
			if m := rbOnlineRE.FindStringSubmatch(c.Detail); m != nil {
				o, _ := strconv.Atoi(m[1])
				v, _ := strconv.Atoi(m[2])
				if voters < 0 || o-(v/2+1) < online-(voters/2+1) {
					online, voters = o, v
				}
			}
			if m := rbGhostRE.FindStringSubmatch(c.Detail); m != nil {
				if n, _ := strconv.Atoi(m[1]); n > ghost {
					ghost = n
				}
			}
		}
	}
	var notes []string
	switch {
	case repl == 0 && total > 0:
		notes = append(notes, "未发现副本队列（quorum/stream）：classic 队列无副本，所在节点故障即不可用，关键业务建议使用 quorum 类型。")
	case repl > 0 && online >= 0:
		maj := voters/2 + 1
		if online < maj {
			notes = append(notes, "副本队列低于多数派：最少在线副本 "+strconv.Itoa(online)+"/"+strconv.Itoa(voters)+"（多数派 "+strconv.Itoa(maj)+"），相关队列已不可用，请恢复节点或调整副本数。")
		} else {
			notes = append(notes, "副本队列满足多数派：最少在线副本 "+strconv.Itoa(online)+"/"+strconv.Itoa(voters)+"。")
		}
	}
	if ghost > 0 {
		notes = append(notes, "存在视图外副本 "+strconv.Itoa(ghost)+" 个：队列成员引用了不在集群视图中的节点，多数派判定已失真，建议重建相关队列副本。")
	}
	return notes
}

// rbPartitionNotes 分区告警：网络分裂期间两侧各自受理，是数据面最危险的形态。
func rbPartitionNotes(hosts []stackkit.ProbeHostRow) []string {
	n := 0
	for _, h := range hosts {
		for _, c := range h.Checks {
			if c.Name != "集群" {
				continue
			}
			if m := rbPartRE.FindStringSubmatch(c.Detail); m != nil {
				if v, err := strconv.Atoi(m[1]); err == nil && v > n {
					n = v
				}
			}
		}
	}
	if n == 0 {
		return nil
	}
	return []string{"检测到分区告警 " + strconv.Itoa(n) + " 项：节点间可能出现网络分裂，请检查节点连通性与时钟同步。"}
}
