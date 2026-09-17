package rabbitmq

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"infra-ops/store/stackkit"
)

// probe.go：RabbitMQ 探活（本机容器 / Ping / 集群视图 / 队列副本，Q3）。
// 全部实现落在本套件内，引擎只做分发与聚合（api/stack/stack_verify_*.go 零分支）。
//
// 容器名固定 rabbitmq-node（node.sh 的 container_name），故 Containers 直接返回，
// 「实例」检查项由 ParseSuite 全权渲染。
//
// 探活数据面（rabbitmq 3.13.7 实测，见 docs/单模式套件方案.md §12）：
//   - rabbitmq-diagnostics -q ping：成功时首行为 "Ping succeeded"；
//   - cluster_status --formatter json：单行大对象，含 running/disk/ram 节点与 partitions；
//   - list_queues name type members：tab 分隔；"Timeout: ..." 与 "Listing queues for vhost ..."
//     两行噪声走 stdout（2>/dev/null 挡不住），解析必须跳过；members 形如 [rabbit@a, rabbit@b]；
//   - rabbitmq-plugins list -e --minimal：每行一个已启用插件名，延迟队列检查项据此判定。
//
// 队列检查的判定面：quorum/stream 队列「在线副本 < 多数派」= 队列已不可用（缩容/宕机后
// 最隐蔽的故障）；成员引用「视图外节点」= 副本残留（幽灵成员），多数派判定已失真。

const compRabbit = "rabbitmq"

// rbCtrLineRE 匹配引擎探活脚本输出的容器状态行（inspect_ctr 'rabbitmq-node'）。
var rbCtrLineRE = regexp.MustCompile(`(?m)^__IO_CTR__([^=\s]+)__=(.*)$`)

// rbCluster cluster_status --formatter json 的裁剪视图（只取判定需要的字段）。
type rbCluster struct {
	RunningNodes []string `json:"running_nodes"`
	DiskNodes    []string `json:"disk_nodes"`
	RamNodes     []string `json:"ram_nodes"`
	// Partitions 分区告警（键为告警名，值为涉及节点）；空对象 = 无分区。
	Partitions map[string][]string `json:"partitions"`
	// Versions 逐节点的版本信息（rabbitmq_version 比镜像标签更准）。
	Versions map[string]struct {
		ErlangVersion   string `json:"erlang_version"`
		RabbitMQVersion string `json:"rabbitmq_version"`
	} `json:"versions"`
}

// Containers 本机期望容器名：rabbitmq-node（每台主机单容器，host 网络）。
func (d *Driver) Containers(ctx stackkit.ProbeCtx) []string { return []string{"rabbitmq-node"} }

// ScriptTail 追加 RabbitMQ 专属探活片段：
//   - PING 取首行（防重试输出污染）；
//   - cluster_status 的 JSON 整块原样回传（供 ParseSuite 解组）；
//   - list_queues 三列（name/type/members）暴露 quorum 副本集合——多数派判定的唯一入口。
func (d *Driver) ScriptTail(ctx stackkit.ProbeCtx) string {
	return `_RB_CTR="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -x 'rabbitmq-node' || true)"
if [ -n "${_RB_CTR}" ]; then
  echo __IO_RB_PING__="$(docker exec "${_RB_CTR}" rabbitmq-diagnostics -q ping 2>&1 | head -n 1)"
  echo __IO_RB_CS_BEGIN__
  docker exec "${_RB_CTR}" rabbitmqctl cluster_status --formatter json 2>/dev/null || true
  echo __IO_RB_CS_END__
  echo __IO_RB_Q_BEGIN__
  docker exec "${_RB_CTR}" rabbitmqctl list_queues name type members 2>/dev/null || true
  echo __IO_RB_Q_END__
  echo __IO_RB_PLUGINS__="$(docker exec "${_RB_CTR}" rabbitmq-plugins list -e --minimal 2>/dev/null | grep -x 'rabbitmq_delayed_message_exchange' || true)"
fi
`
}

// ParseSuite 解析 RabbitMQ 探活输出：实例 / 版本 / PING / 集群 / 队列（delayed_plugin 开关打开时加「延迟队列」）。
// 不设 OK：本主机是否通过由引擎按全部检查项共同判定（ChecksOK）。
func (d *Driver) ParseSuite(ctx stackkit.ProbeCtx, raw string, _ []string) stackkit.ProbeOutcome {
	var out stackkit.ProbeOutcome
	st := ""
	for _, m := range rbCtrLineRE.FindAllStringSubmatch(raw, -1) {
		if strings.TrimSpace(m[1]) == "rabbitmq-node" {
			st = strings.TrimSpace(m[2])
		}
	}
	ok, detail, image, _ := stackkit.ParseCtrStateEx("rabbitmq-node", st)
	if !strings.Contains(detail, "rabbitmq-node") {
		detail = "rabbitmq-node " + detail
	}
	out.Checks = append(out.Checks, stackkit.Check{Name: "实例", Component: compRabbit, OK: ok, Detail: detail})

	var cl rbCluster
	csBlock := stackkit.ExtractBlock(raw, "__IO_RB_CS_BEGIN__", "__IO_RB_CS_END__")
	csOK := csBlock != "" && json.Unmarshal([]byte(csBlock), &cl) == nil
	selfNode := "rabbit@" + ctx.HostIP

	if v := rbVersion(cl, selfNode, image); v != "" {
		out.Checks = append(out.Checks, stackkit.Check{Name: "版本", Component: compRabbit, OK: true, Detail: v})
	}

	ping := strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_RB_PING__="))
	pingOK := strings.Contains(strings.ToLower(ping), "succeeded") || strings.EqualFold(ping, "pong")
	out.Checks = append(out.Checks, stackkit.Check{Name: "PING", Component: compRabbit, OK: pingOK, Detail: stackkit.Nz(ping, "无响应")})

	out.Checks = append(out.Checks, rbClusterCheck(cl, csOK, selfNode))
	out.Checks = append(out.Checks, rbQueueCheck(raw, cl, csOK))
	if rbDelayedWanted(ctx) {
		out.Checks = append(out.Checks, rbDelayedCheck(raw))
	}
	return out
}

// rbDelayedWanted 蓝图开关 delayed_plugin 是否要求安装延迟队列插件（true/yes 均认）。
func rbDelayedWanted(ctx stackkit.ProbeCtx) bool {
	v := strings.ToLower(strings.TrimSpace(stackkit.PickParam(ctx.Params, "delayed_plugin", "")))
	return v == "true" || v == "yes"
}

// rbDelayedCheck 延迟队列插件检查：探活输出的 __IO_RB_PLUGINS__ 行含插件名 = 已启用。
func rbDelayedCheck(raw string) stackkit.Check {
	c := stackkit.Check{Name: "延迟队列", Component: compRabbit}
	if strings.TrimSpace(stackkit.ExtractLine(raw, "__IO_RB_PLUGINS__=")) == "rabbitmq_delayed_message_exchange" {
		c.OK = true
		c.Detail = "插件已启用（x-delayed-message）"
		return c
	}
	c.Detail = "插件未启用"
	return c
}

// rbVersion 版本取集群视图里本机节点的 rabbitmq_version（比镜像标签更准），视图缺失时回落镜像。
func rbVersion(cl rbCluster, selfNode, image string) string {
	if v, ok := cl.Versions[selfNode]; ok && v.RabbitMQVersion != "" {
		if v.ErlangVersion != "" {
			return v.RabbitMQVersion + "（Erlang " + v.ErlangVersion + "）"
		}
		return v.RabbitMQVersion
	}
	return image
}

// rbClusterCheck 集群检查：本机在 running_nodes（可服务）且无分区告警；
// 离线节点数（视图内但未运行）单独提示——被直接停服的节点会长期残留在视图。
func rbClusterCheck(cl rbCluster, csOK bool, selfNode string) stackkit.Check {
	c := stackkit.Check{Name: "集群", Component: compRabbit}
	if !csOK {
		c.Detail = "cluster_status 无响应"
		return c
	}
	inRunning := rbHas(cl.RunningNodes, selfNode)
	c.OK = inRunning && len(cl.Partitions) == 0
	detail := "本机在线"
	if !inRunning {
		detail = "本机不在 running_nodes"
	}
	detail += " · running=" + strconv.Itoa(len(cl.RunningNodes))
	if n := rbStale(cl); n > 0 {
		detail += " · 离线节点=" + strconv.Itoa(n)
	}
	detail += " · 分区告警=" + strconv.Itoa(len(cl.Partitions))
	c.Detail = detail
	return c
}

// rbQueueCheck 队列检查：quorum/stream 队列的在线副本必须满足多数派，且无视图外成员。
func rbQueueCheck(raw string, cl rbCluster, csOK bool) stackkit.Check {
	c := stackkit.Check{Name: "队列", Component: compRabbit}
	if !strings.Contains(raw, "__IO_RB_Q_BEGIN__") {
		c.Detail = "list_queues 无响应"
		return c
	}
	queues := parseQueues(stackkit.ExtractBlock(raw, "__IO_RB_Q_BEGIN__", "__IO_RB_Q_END__"))
	repl, classic := 0, 0
	ghost := 0
	minOnline, minVoters, slack := 0, 0, 0
	first := true
	running, view := rbSet(cl.RunningNodes), rbView(cl)
	for _, q := range queues {
		if q.typ != "quorum" && q.typ != "stream" {
			classic++
			continue
		}
		repl++
		online := 0
		for _, m := range q.members {
			if running[m] {
				online++
			}
			if !view[m] {
				ghost++
			}
		}
		s := online - (len(q.members)/2 + 1)
		if first || s < slack {
			minOnline, minVoters, slack, first = online, len(q.members), s, false
		}
	}
	detail := "共 " + strconv.Itoa(len(queues)) + "（副本队列 " + strconv.Itoa(repl) + " · classic " + strconv.Itoa(classic) + "）"
	if repl > 0 {
		detail += " · 最少在线 " + strconv.Itoa(minOnline) + "/" + strconv.Itoa(minVoters)
		if slack < 0 {
			detail += "（低于多数派 " + strconv.Itoa(minVoters/2+1) + "）"
		}
	}
	if ghost > 0 {
		detail += " · 视图外 " + strconv.Itoa(ghost)
	}
	if !csOK {
		detail += " · 无集群视图，未核对副本"
	}
	c.Detail = detail
	c.OK = csOK && slack >= 0 && ghost == 0
	return c
}

// ---------- 解析助手 ----------

type rbQueue struct {
	name    string
	typ     string
	members []string
}

// parseQueues 解析 rabbitmqctl list_queues name type members 的 stdout（按名字排序，快照稳定）。
func parseQueues(block string) []rbQueue {
	var out []rbQueue
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "Timeout:") || strings.HasPrefix(line, "Listing queues") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 || (f[0] == "name" && f[1] == "type") {
			continue
		}
		q := rbQueue{name: f[0], typ: f[1]}
		if len(f) > 2 {
			q.members = rbMembers(f[2])
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// rbMembers 解析 `[rabbit@a, rabbit@b]` 形式的副本集合（classic 队列为空）。
func rbMembers(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// rbHas 节点名是否在列表中（避免空串误配）。
func rbHas(nodes []string, want string) bool {
	return want != "" && stackkit.Contains(nodes, want)
}

// rbSet 节点列表 → 集合。
func rbSet(nodes []string) map[string]bool {
	m := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		m[n] = true
	}
	return m
}

// rbView 集群视图全量节点名（running ∪ disk ∪ ram）——判定「视图外副本」的基准。
func rbView(cl rbCluster) map[string]bool {
	m := rbSet(cl.RunningNodes)
	for _, n := range cl.DiskNodes {
		m[n] = true
	}
	for _, n := range cl.RamNodes {
		m[n] = true
	}
	return m
}

// rbStale 视图内但未运行的节点数（停止/失联，含被直接停服的幽灵成员）。
func rbStale(cl rbCluster) int {
	n := 0
	for _, node := range append(append([]string{}, cl.DiskNodes...), cl.RamNodes...) {
		if !rbHas(cl.RunningNodes, node) {
			n++
		}
	}
	return n
}
