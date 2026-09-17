package rabbitmq

import (
	"strings"
	"testing"

	"infra-ops/store/stackkit"
)

// probe_test.go：rabbitmq 探活解析测试。
//
// 夹具形状来自 rabbitmq:3.13.7 本机双节点集群的真实探活输出（E2E 采样，
// 见 docs/单模式套件方案.md §12）：cluster_status --formatter json 为单行对象、
// list_queues 前置 "Timeout/" 与 "Listing queues" 两行 stdout 噪声、tab 分隔三列、
// members 为 [rabbit@a, rabbit@b]；节点名用生产风格的 IP 字面量（rabbit@10.0.3.x）。

// rbCSJSON 集群视图（截取实测输出中判定所需字段：disk/ram/running/versions/partitions）。
const rbCSJSON = `{"disk_nodes":["rabbit@10.0.3.1","rabbit@10.0.3.2"],"ram_nodes":[],` +
	`"running_nodes":["rabbit@10.0.3.2","rabbit@10.0.3.1"],` +
	`"versions":{"rabbit@10.0.3.1":{"erlang_version":"26.2.5.16","rabbitmq_version":"3.13.7"},` +
	`"rabbit@10.0.3.2":{"erlang_version":"26.2.5.16","rabbitmq_version":"3.13.7"}},` +
	`"partitions":{}}`

// rbRaw 健康双节点的完整探活输出。
var rbRaw = strings.Join([]string{
	"__IO_CTR__rabbitmq-node__=running|1||rabbitmq:3.13-management|2026-09-15T09:55:21Z",
	"__IO_RB_PING__=Ping succeeded",
	"__IO_RB_CS_BEGIN__",
	rbCSJSON,
	"__IO_RB_CS_END__",
	"__IO_RB_Q_BEGIN__",
	"Timeout: 60.0 seconds ...",
	"Listing queues for vhost / ...",
	"name\ttype\tmembers",
	"cq\tclassic\t",
	"qq\tquorum\t[rabbit@10.0.3.2, rabbit@10.0.3.1]",
	"__IO_RB_Q_END__",
	"__IO_RB_PLUGINS__=rabbitmq_delayed_message_exchange",
	"__IO_END__",
}, "\n")

func rbCtx(ip string) stackkit.ProbeCtx {
	return stackkit.ProbeCtx{
		Mode: "cluster", Role: "master", HostIP: ip,
		Params: map[string]string{"amqp_port": "5672", "mgmt_port": "15672"},
	}
}

func rbCheck(t *testing.T, checks []stackkit.Check, name string) stackkit.Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("未找到检查项 %q（实际：%+v）", name, checks)
	return stackkit.Check{}
}

func TestRabbitMQContainersIsFixed(t *testing.T) {
	got := (&Driver{}).Containers(rbCtx("10.0.3.1"))
	if len(got) != 1 || got[0] != "rabbitmq-node" {
		t.Fatalf("Containers = %v, want [rabbitmq-node]", got)
	}
}

func TestScriptTail(t *testing.T) {
	tail := (&Driver{}).ScriptTail(rbCtx("10.0.3.1"))
	for _, want := range []string{
		"grep -x 'rabbitmq-node'",
		"rabbitmq-diagnostics -q ping",
		"__IO_RB_CS_BEGIN__",
		"cluster_status --formatter json",
		"list_queues name type members",
		"list -e --minimal",
		"__IO_RB_PLUGINS__",
	} {
		if !strings.Contains(tail, want) {
			t.Fatalf("ScriptTail 缺少 %q:\n%s", want, tail)
		}
	}
}

func TestParseSuiteClusterHealthy(t *testing.T) {
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), rbRaw, nil)
	if c := rbCheck(t, out.Checks, "实例"); !c.OK || c.Detail != "rabbitmq-node running" {
		t.Fatalf("实例 = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "版本"); !c.OK || c.Detail != "3.13.7（Erlang 26.2.5.16）" {
		t.Fatalf("版本 = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "PING"); !c.OK || c.Detail != "Ping succeeded" {
		t.Fatalf("PING = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "集群"); !c.OK || c.Detail != "本机在线 · running=2 · 分区告警=0" {
		t.Fatalf("集群 = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "队列"); !c.OK || c.Detail != "共 2（副本队列 1 · classic 1） · 最少在线 2/2" {
		t.Fatalf("队列 = %+v", c)
	}
}

// 一台节点宕机（running 视图只剩本机）：本机仍在跑，但 quorum 2 副本栈只剩 1 票。
func TestParseSuiteQuorumMinority(t *testing.T) {
	raw := strings.Replace(rbRaw,
		`"running_nodes":["rabbit@10.0.3.2","rabbit@10.0.3.1"]`,
		`"running_nodes":["rabbit@10.0.3.1"]`, 1)
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), raw, nil)
	if c := rbCheck(t, out.Checks, "集群"); !c.OK || c.Detail != "本机在线 · running=1 · 离线节点=1 · 分区告警=0" {
		t.Fatalf("集群 = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "队列"); c.OK || c.Detail != "共 2（副本队列 1 · classic 1） · 最少在线 1/2（低于多数派 2）" {
		t.Fatalf("队列 = %+v", c)
	}
}

// 副本集合含视图外节点（幽灵成员）：多数派判定失真。
func TestParseSuiteGhostReplica(t *testing.T) {
	raw := strings.Replace(rbRaw,
		"[rabbit@10.0.3.2, rabbit@10.0.3.1]",
		"[rabbit@10.0.3.2, rabbit@10.0.3.1, rabbit@10.0.3.9]", 1)
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), raw, nil)
	if c := rbCheck(t, out.Checks, "队列"); c.OK || c.Detail != "共 2（副本队列 1 · classic 1） · 最少在线 2/3 · 视图外 1" {
		t.Fatalf("队列 = %+v", c)
	}
}

// 本机被移出集群（forget 后仍被探活）：running 视图不含本机。
func TestParseSuiteNotInRunning(t *testing.T) {
	raw := strings.Replace(rbRaw,
		`"running_nodes":["rabbit@10.0.3.2","rabbit@10.0.3.1"]`,
		`"running_nodes":["rabbit@10.0.3.2"]`, 1)
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), raw, nil)
	if c := rbCheck(t, out.Checks, "集群"); c.OK || c.Detail != "本机不在 running_nodes · running=1 · 离线节点=1 · 分区告警=0" {
		t.Fatalf("集群 = %+v", c)
	}
}

func TestParseSuitePingFail(t *testing.T) {
	raw := strings.Replace(rbRaw, "__IO_RB_PING__=Ping succeeded",
		"__IO_RB_PING__=Error: unable to connect to node rabbit@10.0.3.1: nodedown", 1)
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), raw, nil)
	if c := rbCheck(t, out.Checks, "PING"); c.OK || !strings.Contains(c.Detail, "nodedown") {
		t.Fatalf("PING = %+v", c)
	}
}

// 容器缺失：后续探测块整体缺席，检查项逐一明确失败（不产生解析噪声）。
func TestParseSuiteContainerMissing(t *testing.T) {
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), "__IO_CTR__rabbitmq-node__=missing\n__IO_END__", nil)
	if c := rbCheck(t, out.Checks, "实例"); c.OK || c.Detail != "rabbitmq-node 不存在" {
		t.Fatalf("实例 = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "PING"); c.OK || c.Detail != "无响应" {
		t.Fatalf("PING = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "集群"); c.OK || c.Detail != "cluster_status 无响应" {
		t.Fatalf("集群 = %+v", c)
	}
	if c := rbCheck(t, out.Checks, "队列"); c.OK || c.Detail != "list_queues 无响应" {
		t.Fatalf("队列 = %+v", c)
	}
	for _, c := range out.Checks {
		if c.Name == "版本" {
			t.Fatalf("容器缺失不应通过镜像回落出「版本」检查项：%+v", c)
		}
	}
}

// 队列为空的集群：队列检查项正常（0 个队列不是故障）。
func TestParseSuiteNoQueues(t *testing.T) {
	raw := strings.Replace(rbRaw,
		"name\ttype\tmembers\ncq\tclassic\t\nqq\tquorum\t[rabbit@10.0.3.2, rabbit@10.0.3.1]",
		"name\ttype\tmembers", 1)
	out := (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), raw, nil)
	if c := rbCheck(t, out.Checks, "队列"); !c.OK || c.Detail != "共 0（副本队列 0 · classic 0）" {
		t.Fatalf("队列 = %+v", c)
	}
}

// 延迟队列插件：开关打开时才出示检查项；已启用/未启用两态，均不阻断其它检查项。
func TestParseSuiteDelayedPlugin(t *testing.T) {
	onCtx := rbCtx("10.0.3.1")
	onCtx.Params["delayed_plugin"] = "true"
	out := (&Driver{}).ParseSuite(onCtx, rbRaw, nil)
	if c := rbCheck(t, out.Checks, "延迟队列"); !c.OK || c.Detail != "插件已启用（x-delayed-message）" {
		t.Fatalf("延迟队列 = %+v", c)
	}
	raw := strings.Replace(rbRaw,
		"__IO_RB_PLUGINS__=rabbitmq_delayed_message_exchange", "__IO_RB_PLUGINS__=", 1)
	out = (&Driver{}).ParseSuite(onCtx, raw, nil)
	if c := rbCheck(t, out.Checks, "延迟队列"); c.OK || c.Detail != "插件未启用" {
		t.Fatalf("延迟队列 = %+v", c)
	}
	// 开关未打开：不出示检查项（存量实例不背插件预期）。
	out = (&Driver{}).ParseSuite(rbCtx("10.0.3.1"), rbRaw, nil)
	for _, c := range out.Checks {
		if c.Name == "延迟队列" {
			t.Fatalf("开关未打开不应出示延迟队列检查项：%+v", c)
		}
	}
}

func TestParseQueues(t *testing.T) {
	block := "Timeout: 60.0 seconds ...\n" +
		"Listing queues for vhost / ...\n" +
		"name\ttype\tmembers\n" +
		"b\tquorum\t[rabbit@x]\n" +
		"a\tclassic\t\n"
	qs := parseQueues(block)
	if len(qs) != 2 {
		t.Fatalf("解析出 %d 个队列，want 2：%+v", len(qs), qs)
	}
	if qs[0].name != "a" || qs[0].typ != "classic" || len(qs[0].members) != 0 {
		t.Fatalf("qs[0] = %+v", qs[0])
	}
	if qs[1].name != "b" || qs[1].typ != "quorum" || len(qs[1].members) != 1 || qs[1].members[0] != "rabbit@x" {
		t.Fatalf("qs[1] = %+v", qs[1])
	}
}
