package kafka

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// kraftRaw 一份真实的 KRaft 探活输出（容器名带序号，故必须由 ScriptTail 现取）。
const kraftRaw = `__IO_BEGIN__
__IO_COMPOSE_BEGIN__
kafka-1=running
__IO_COMPOSE_END__
__IO_CTR__kafka-1__=running|1||apache/kafka:3.7.0|2026-09-15T02:00:00Z
__IO_QUORUM_BEGIN__
ClusterId:              aaaBBBccc
LeaderId:               1
LeaderEpoch:            3
HighWatermark:          1024
MaxFollowerLag:         0
MaxFollowerLagTimeMs:   -1
CurrentVoters:          [1,2,3]
CurrentObservers:       []
__IO_QUORUM_END__
__IO_END__
`

const zkRaw = `__IO_BEGIN__
__IO_COMPOSE_BEGIN__
kafka-1=running
kafka-zk-1=running
__IO_COMPOSE_END__
__IO_CTR__kafka-1__=running|1||apache/kafka:3.7.0|2026-09-15T02:00:00Z
__IO_CTR__kafka-zk-1__=running|1||zookeeper:3.9|2026-09-15T02:00:00Z
__IO_ZK_SRVR_BEGIN__
Zookeeper version: 3.9.2-abc, built on 2024-01-01
Latency min/avg/max: 0/0.0/0
Connections: 1
Outstanding: 0
Zxid: 0x5
Mode: follower
Node count: 12
__IO_ZK_SRVR_END__
__IO_END__
`

func probeCtx(mode string, params map[string]string) stackkit.ProbeCtx {
	if params == nil {
		params = map[string]string{"port": "9092", "home_dir": "/data/kafka-" + mode}
	}
	return stackkit.ProbeCtx{Mode: mode, Role: "node", HostIP: "10.0.0.1", Params: params}
}

func findCheck(checks []stackkit.Check, name, comp string) *stackkit.Check {
	for i := range checks {
		if checks[i].Name == name && (comp == "" || checks[i].Component == comp) {
			return &checks[i]
		}
	}
	return nil
}

// Kraft：容器名含序号 ⇒ Containers 必须为空（名字由 ScriptTail 现取）。
func TestContainersIsEmpty(t *testing.T) {
	if got := New().Containers(probeCtx("kraft", nil)); len(got) != 0 {
		t.Fatalf("容器名不可预知，应返回空: %v", got)
	}
}

// Kraft 探活：实例（kafka）/ 版本 / 仲裁 三项，且 component 均归属 kafka。
func TestParseSuiteKraft(t *testing.T) {
	out := New().ParseSuite(probeCtx("kraft", nil), kraftRaw, nil)
	if len(out.Checks) != 3 {
		t.Fatalf("期望 3 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	inst := findCheck(out.Checks, "实例", compKafka)
	if inst == nil || !inst.OK {
		t.Fatalf("实例检查缺失或未通过: %+v", out.Checks)
	}
	if !strings.Contains(inst.Detail, "kafka-1") {
		t.Fatalf("实例明细应含容器名: %q", inst.Detail)
	}
	if ver := findCheck(out.Checks, "版本", compKafka); ver == nil || ver.Detail != "apache/kafka:3.7.0" {
		t.Fatalf("版本检查应取镜像: %+v", out.Checks)
	}
	q := findCheck(out.Checks, "仲裁", compKafka)
	if q == nil || !q.OK {
		t.Fatalf("有 leader 时仲裁应通过: %+v", out.Checks)
	}
	if !strings.Contains(q.Detail, "voters=3") || !strings.Contains(q.Detail, "leader=1") {
		t.Fatalf("仲裁明细: %q", q.Detail)
	}
	// 未实测角色 ⇒ 交还引擎回落期望角色
	if out.LiveRoleFromOutput {
		t.Fatal("kafka 无实测角色，不应声明 LiveRoleFromOutput")
	}
}

// 无 leader（如缩容后失去多数派）⇒ 仲裁必须判失败。
func TestParseSuiteKraftNoLeader(t *testing.T) {
	raw := strings.Replace(kraftRaw, "LeaderId:               1", "LeaderId:               ", 1)
	out := New().ParseSuite(probeCtx("kraft", nil), raw, nil)
	q := findCheck(out.Checks, "仲裁", compKafka)
	if q == nil || q.OK {
		t.Fatalf("无 leader 应判失败: %+v", out.Checks)
	}
	if stackkit.ChecksOK(out.Checks) {
		t.Fatal("ChecksOK 应给出本机不通过")
	}
}

// ZK：一台主机两个容器 ⇒ 两个组件各有「实例」，外加 ZK 的「集群」检查。
func TestParseSuiteZK(t *testing.T) {
	out := New().ParseSuite(probeCtx("zk", nil), zkRaw, nil)
	if len(out.Checks) != 4 {
		t.Fatalf("期望 4 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	broker := findCheck(out.Checks, "实例", compKafka)
	zkInst := findCheck(out.Checks, "实例", compZK)
	if broker == nil || zkInst == nil {
		t.Fatalf("两个组件应各有实例检查: %+v", out.Checks)
	}
	if !strings.Contains(zkInst.Detail, "kafka-zk-1") {
		t.Fatalf("ZK 实例明细: %q", zkInst.Detail)
	}
	cs := findCheck(out.Checks, "集群", compZK)
	if cs == nil || !cs.OK {
		t.Fatalf("follower 应判通过: %+v", out.Checks)
	}
	if !strings.Contains(cs.Detail, "mode=follower") || !strings.Contains(cs.Detail, "nodes=12") {
		t.Fatalf("集群明细: %q", cs.Detail)
	}
	// 版本只取 broker 镜像
	if ver := findCheck(out.Checks, "版本", compKafka); ver == nil || !strings.Contains(ver.Detail, "kafka") {
		t.Fatalf("版本应取 broker 镜像: %+v", out.Checks)
	}
}

// ZK 无响应（srvr 块为空）⇒ 集群检查必须失败，不能因为「没解析到」而静默通过。
func TestParseSuiteZKNoResponse(t *testing.T) {
	out := New().ParseSuite(probeCtx("zk", nil), zkRaw[:strings.Index(zkRaw, "__IO_ZK_SRVR_BEGIN__")], nil)
	cs := findCheck(out.Checks, "集群", compZK)
	if cs == nil || cs.OK {
		t.Fatalf("无响应应判失败: %+v", out.Checks)
	}
}

// Containers/ScriptTail 契约：tail 里必须现取容器名并复用 inspect_ctr。
func TestScriptTail(t *testing.T) {
	kraft := New().ScriptTail(probeCtx("kraft", nil))
	if !strings.Contains(kraft, "inspect_ctr") || !strings.Contains(kraft, "kafka-metadata-quorum.sh") {
		t.Fatalf("kraft tail 缺容器巡检或仲裁探测:\n%s", kraft)
	}
	if strings.Contains(kraft, "srvr") {
		t.Fatal("kraft 模式不应探测 ZK")
	}
	zk := New().ScriptTail(probeCtx("zk", map[string]string{"zk_port": "2181"}))
	if !strings.Contains(zk, "srvr") || !strings.Contains(zk, "_ZK_PORT='2181'") {
		t.Fatalf("zk tail 缺 ZK 探测:\n%s", zk)
	}
	if strings.Contains(zk, "kafka-metadata-quorum.sh") {
		t.Fatal("zk 模式不应探测 KRaft 仲裁")
	}
	// 容器名只能来自 docker ps（探活上下文没有运行期序号）
	if !strings.Contains(zk, "docker ps") {
		t.Fatal("tail 应现取容器名")
	}
}

// Summary：voters 3 台而只剩 1 台通过 ⇒ 必须给出「仲裁异常」（骨架据此把整体判为不通过）。
func TestSummaryQuorumLost(t *testing.T) {
	hosts := []stackkit.ProbeHostRow{
		{HostIP: "10.0.0.1", OK: true, Checks: []stackkit.Check{{Name: "仲裁", Component: compKafka, OK: false, Detail: "leader=1 · voters=3"}}},
		{HostIP: "10.0.0.2", OK: false},
		{HostIP: "10.0.0.3", OK: false},
	}
	notes, hint := New().Summary(probeCtx("kraft", nil), hosts)
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "仲裁异常") {
		t.Fatalf("应给出仲裁异常: %v", notes)
	}
	if !strings.Contains(joined, "多数派 2") {
		t.Fatalf("应给出多数派所需台数: %v", notes)
	}
	if !strings.Contains(hint, "kafka-topics.sh") || !strings.Contains(hint, "10.0.0.1:9092") {
		t.Fatalf("客户端命令: %q", hint)
	}
}

// Summary：voters 3 台、3 台通过 ⇒ 正常；偶数 voters 给建议但不算异常。
func TestSummaryQuorumOK(t *testing.T) {
	mk := func(n int, ok bool) []stackkit.ProbeHostRow {
		out := make([]stackkit.ProbeHostRow, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, stackkit.ProbeHostRow{
				HostIP: "10.0.0.1", OK: ok,
				Checks: []stackkit.Check{{Name: "仲裁", Component: compKafka, OK: ok, Detail: "leader=1 · voters=3"}},
			})
		}
		return out
	}
	notes, _ := New().Summary(probeCtx("kraft", nil), mk(3, true))
	joined := strings.Join(notes, " | ")
	if !strings.Contains(joined, "仲裁正常") || strings.Contains(joined, "异常") {
		t.Fatalf("3 台应判正常: %v", notes)
	}
	// voters=4（偶数）：4 台都在线，提示建议，但不含「异常」关键词（否则骨架会误判整体失败）
	even := make([]stackkit.ProbeHostRow, 0, 4)
	for i := 0; i < 4; i++ {
		even = append(even, stackkit.ProbeHostRow{HostIP: "10.0.0.1", OK: true,
			Checks: []stackkit.Check{{Name: "仲裁", Component: compKafka, OK: true, Detail: "leader=1 · voters=4"}}})
	}
	notes, _ = New().Summary(probeCtx("kraft", nil), even)
	joined = strings.Join(notes, " | ")
	if !strings.Contains(joined, "偶数") || strings.Contains(joined, "异常") {
		t.Fatalf("偶数 voters 只应给建议: %v", notes)
	}
}

// Summary：ZK 的 leader 必须唯一。
func TestSummaryZKLeader(t *testing.T) {
	row := func(mode string) stackkit.ProbeHostRow {
		return stackkit.ProbeHostRow{HostIP: "10.0.0.1", OK: true,
			Checks: []stackkit.Check{{Name: "集群", Component: compZK, OK: true, Detail: "mode=" + mode}}}
	}
	notes, _ := New().Summary(probeCtx("zk", nil), []stackkit.ProbeHostRow{row("leader"), row("follower"), row("follower")})
	if joined := strings.Join(notes, " | "); !strings.Contains(joined, "1 leader + 2 follower") {
		t.Fatalf("%v", notes)
	}
	notes, _ = New().Summary(probeCtx("zk", nil), []stackkit.ProbeHostRow{row("follower"), row("follower")})
	if joined := strings.Join(notes, " | "); !strings.Contains(joined, "未选出 leader") {
		t.Fatalf("%v", notes)
	}
}

// Summary：开启 enable_ui 却探不到 kafka-ui ⇒ 必须点出来（否则用户以为 UI 装好了）。
func TestSummaryUIMissing(t *testing.T) {
	checks := []stackkit.Check{{Name: "实例", Component: compKafka, OK: true, Detail: "kafka-1 running"}}
	notes, _ := New().Summary(probeCtx("kraft", map[string]string{"enable_ui": "true"}),
		[]stackkit.ProbeHostRow{{HostIP: "10.0.0.1", OK: true, Checks: checks}})
	if joined := strings.Join(notes, " | "); !strings.Contains(joined, "KafkaUI 异常") {
		t.Fatalf("应提示 UI 缺失: %v", notes)
	}
	// 容器在 ⇒ 不再提示
	withUI := append(checks, stackkit.Check{Name: "实例", Component: compUI, OK: true, Detail: "kafka-ui running"})
	notes, _ = New().Summary(probeCtx("kraft", map[string]string{"enable_ui": "true"}),
		[]stackkit.ProbeHostRow{{HostIP: "10.0.0.1", OK: true, Checks: withUI}})
	if joined := strings.Join(notes, " | "); strings.Contains(joined, "KafkaUI") {
		t.Fatalf("UI 在跑就不该提示: %v", notes)
	}
}

// 端点：组件归属 + 端口取参数 + UI 只给首台。
func TestEndpoints(t *testing.T) {
	d := New()
	// kraft 首台、未开 UI
	out := d.Endpoints(stackkit.EndpointCtx{
		Mode: "kraft", Host: model.StackInstanceHost{HostIP: "10.0.0.1", Role: "node", Seq: 1},
		Params: map[string]string{"port": "9092"}, InstName: "Kafka",
	})
	if len(out) != 1 || out[0].Component != compKafka || out[0].URL != "kafka://10.0.0.1:9092" {
		t.Fatalf("%+v", out)
	}
	// zk 两台：都应给出 kafka + zookeeper 两个端点
	out = d.Endpoints(stackkit.EndpointCtx{
		Mode: "zk", Host: model.StackInstanceHost{HostIP: "10.0.0.2", Role: "node", Seq: 2},
		Params: map[string]string{"port": "9092", "zk_port": "2181"}, InstName: "Kafka",
	})
	if len(out) != 2 || out[1].Component != compZK || out[1].URL != "zookeeper://10.0.0.2:2181" {
		t.Fatalf("%+v", out)
	}
	// 开了 UI：首台多一条 http 端点，第二台没有
	uiParams := map[string]string{"port": "9092", "enable_ui": "true", "ui_port": "8080"}
	first := d.Endpoints(stackkit.EndpointCtx{Mode: "kraft", Host: model.StackInstanceHost{HostIP: "10.0.0.1", Seq: 1}, Params: uiParams})
	second := d.Endpoints(stackkit.EndpointCtx{Mode: "kraft", Host: model.StackInstanceHost{HostIP: "10.0.0.2", Seq: 2}, Params: uiParams})
	if len(first) != 2 || first[1].Component != compUI || first[1].URL != "http://10.0.0.1:8080" {
		t.Fatalf("首台应含 UI: %+v", first)
	}
	if len(second) != 1 {
		t.Fatalf("非首台不应含 UI: %+v", second)
	}
}
