package rabbitmq

import (
	"strings"
	"testing"

	"infra-ops/store/stackkit"
)

// summary_test.go：探活汇总（Summary）聚合结论测试。
// 队列/集群检查项的 detail 按 probe.go 的真实产出格式构造（含聚合标记）。

// rbRow 单主机摘要行（ok=false 表示该主机存在失败检查项）。
func rbRow(ip string, ok bool, queueDetail, clusterDetail string) stackkit.ProbeHostRow {
	row := stackkit.ProbeHostRow{HostID: 1, HostIP: ip, Role: "master", OK: ok}
	if queueDetail != "" {
		row.Checks = append(row.Checks, stackkit.Check{Name: "队列", Component: compRabbit, OK: ok, Detail: queueDetail})
	}
	if clusterDetail != "" {
		row.Checks = append(row.Checks, stackkit.Check{Name: "集群", Component: compRabbit, OK: ok, Detail: clusterDetail})
	}
	return row
}

func rbHasNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// 健康双节点：队列满足多数派；正常汇总不得含引擎的关键词（否则整体被翻判失败）。
func TestSummaryHealthy(t *testing.T) {
	rows := []stackkit.ProbeHostRow{
		rbRow("10.0.3.1", true, "共 2（副本队列 1 · classic 1） · 最少在线 2/2", "本机在线 · running=2 · 分区告警=0"),
		rbRow("10.0.3.2", true, "共 2（副本队列 1 · classic 1） · 最少在线 2/2", "本机在线 · running=2 · 分区告警=0"),
	}
	notes, hint := (&Driver{}).Summary(rbCtx("10.0.3.1"), rows)
	if hint != "http://10.0.3.1:15672" {
		t.Fatalf("hint = %q", hint)
	}
	if !rbHasNote(notes, "副本队列满足多数派") {
		t.Fatalf("notes = %v", notes)
	}
	for _, n := range notes {
		for _, bad := range []string{"异常", "未就绪", "不符"} {
			if strings.Contains(n, bad) {
				t.Fatalf("正常汇总不得含关键字 %q：%s", bad, n)
			}
		}
	}
}

// quorum 副本低于多数派：最隐蔽的故障（集群在跑、队列已不可用）。
func TestSummaryQuorumMinority(t *testing.T) {
	rows := []stackkit.ProbeHostRow{
		rbRow("10.0.3.1", false, "共 2（副本队列 1 · classic 1） · 最少在线 1/2（低于多数派 2）", "本机在线 · running=1 · 离线节点=1 · 分区告警=0"),
	}
	notes, _ := (&Driver{}).Summary(rbCtx("10.0.3.1"), rows)
	if !rbHasNote(notes, "副本队列低于多数派") {
		t.Fatalf("notes = %v", notes)
	}
}

// 全部是无副本队列：给出 classic 单点故障面的提示。
func TestSummaryNoReplQueues(t *testing.T) {
	rows := []stackkit.ProbeHostRow{
		rbRow("10.0.3.1", true, "共 2（副本队列 0 · classic 2）", "本机在线 · running=2 · 分区告警=0"),
	}
	notes, _ := (&Driver{}).Summary(rbCtx("10.0.3.1"), rows)
	if !rbHasNote(notes, "未发现副本队列") {
		t.Fatalf("notes = %v", notes)
	}
}

// 多机聚合取最危险者：一台给正常值、一台给少数派值，结论应为少数派。
func TestSummaryAggregatesWorst(t *testing.T) {
	rows := []stackkit.ProbeHostRow{
		rbRow("10.0.3.1", true, "共 2（副本队列 1 · classic 1） · 最少在线 2/2", "本机在线 · running=2 · 分区告警=0"),
		rbRow("10.0.3.2", false, "共 2（副本队列 1 · classic 1） · 最少在线 1/2（低于多数派 2）", "本机在线 · running=2 · 分区告警=0"),
	}
	notes, _ := (&Driver{}).Summary(rbCtx("10.0.3.1"), rows)
	if !rbHasNote(notes, "最少在线副本 1/2") {
		t.Fatalf("notes = %v", notes)
	}
}

// 视图外副本 + 分区告警：两条结论都要出。
func TestSummaryGhostAndPartition(t *testing.T) {
	rows := []stackkit.ProbeHostRow{
		rbRow("10.0.3.1", false, "共 2（副本队列 1 · classic 1） · 最少在线 2/3 · 视图外 1", "本机在线 · running=2 · 分区告警=1"),
	}
	notes, _ := (&Driver{}).Summary(rbCtx("10.0.3.1"), rows)
	if !rbHasNote(notes, "存在视图外副本 1 个") {
		t.Fatalf("notes = %v", notes)
	}
	if !rbHasNote(notes, "分区告警 1 项") {
		t.Fatalf("notes = %v", notes)
	}
}

// 无检查项（主机未产探活结果）：只给基础 note，不臆造队列结论。
func TestSummaryNoChecks(t *testing.T) {
	rows := []stackkit.ProbeHostRow{{HostID: 1, HostIP: "10.0.3.1", Role: "master"}}
	notes, hint := (&Driver{}).Summary(rbCtx("10.0.3.1"), rows)
	if hint != "http://10.0.3.1:15672" {
		t.Fatalf("hint = %q", hint)
	}
	if rbHasNote(notes, "副本队列") || rbHasNote(notes, "分区告警") {
		t.Fatalf("无检查项不应给聚合结论：%v", notes)
	}
}
