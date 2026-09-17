package rocketmq

import (
	"strings"
	"testing"

	"infra-ops/store/stackkit"
)

// regRow 造一条带「注册」检查项的主机行（Summary 的副本聚合素材）。
func regRow(ip, role, detail string, ok bool) stackkit.ProbeHostRow {
	return stackkit.ProbeHostRow{
		HostIP: ip, Role: role, OK: ok,
		Checks: []stackkit.Check{{Name: "注册", Component: compRocket, OK: ok, Detail: detail}},
	}
}

// 健康两机：接入说明 + 单点说明 + 1 个 Slave；hint 指向 master 的 NameServer。
func TestSummaryHealthy(t *testing.T) {
	hosts := []stackkit.ProbeHostRow{
		regRow("172.31.98.11", "master", "172.31.98.11:10911 · BID=0（master）", true),
		regRow("172.31.98.12", "worker", "172.31.98.12:10911 · BID=1（worker）", true),
	}
	notes, hint := New().Summary(probeCtx("master", "172.31.98.11"), hosts)
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "客户端先连 NameServer") || !strings.Contains(joined, "172.31.98.11:9876") {
		t.Fatalf("接入说明: %v", notes)
	}
	if !strings.Contains(joined, "NameServer 仅部署在 master 节点") || !strings.Contains(joined, "单点") {
		t.Fatalf("单点说明: %v", notes)
	}
	if !strings.Contains(joined, "1 个 Slave（异步复制）") {
		t.Fatalf("副本结论: %v", notes)
	}
	if hint != "rocketmq://172.31.98.11:9876" {
		t.Fatalf("hint: %q", hint)
	}
	// 汇总文案不得触发引擎的失败关键字（异常 / 未就绪 / 不符）
	for _, n := range notes {
		if strings.Contains(n, "异常") || strings.Contains(n, "未就绪") || strings.Contains(n, "不符") {
			t.Fatalf("正常汇总不应含失败关键字: %q", n)
		}
	}
}

// 仅 master（无 Slave）：副本结论按无副本形态提示。
func TestSummaryNoSlave(t *testing.T) {
	hosts := []stackkit.ProbeHostRow{
		regRow("172.31.98.11", "master", "172.31.98.11:10911 · BID=0（master）", true),
	}
	notes, _ := New().Summary(probeCtx("master", "172.31.98.11"), hosts)
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "未发现 Slave（无副本）") {
		t.Fatalf("无副本结论: %v", notes)
	}
}

// 无注册检查项（全机未注册 / brokerStatus 均失败）：不下副本结论，单点说明保留。
func TestSummaryNoRegistered(t *testing.T) {
	hosts := []stackkit.ProbeHostRow{
		{HostIP: "172.31.98.11", Role: "master", OK: false},
	}
	notes, hint := New().Summary(probeCtx("master", "172.31.98.11"), hosts)
	joined := strings.Join(notes, " | ")
	if strings.Contains(joined, "Slave") {
		t.Fatalf("未注册时不应有副本结论: %v", notes)
	}
	if !strings.Contains(joined, "NameServer 仅部署在 master 节点") {
		t.Fatalf("单点说明应保留: %v", notes)
	}
	if hint != "rocketmq://172.31.98.11:9876" {
		t.Fatalf("hint: %q", hint)
	}
}

// 自定义端口 + master 缺失：hint 用参数端口，masterIP 回落首台。
func TestSummaryPortsFallback(t *testing.T) {
	ctx := probeCtx("master", "172.31.98.11")
	ctx.Params = map[string]string{"ns_port": "19876", "broker_port": "20911"}
	hosts := []stackkit.ProbeHostRow{{HostIP: "172.31.98.11", Role: "worker"}}
	notes, hint := New().Summary(ctx, hosts)
	if hint != "rocketmq://172.31.98.11:19876" {
		t.Fatalf("hint: %q", hint)
	}
	if !strings.Contains(strings.Join(notes, " | "), "20911") {
		t.Fatalf("Broker 端口应取参数: %v", notes)
	}
}
