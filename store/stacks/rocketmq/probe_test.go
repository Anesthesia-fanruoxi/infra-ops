package rocketmq

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// 探活夹具：apache/rocketmq:4.9.7 真机实测形态（docs/单模式套件方案.md §12）。
// 拓扑：master=172.31.98.11（BID=0）、slave=172.31.98.12（BID=1）、
// NameServer=172.31.98.10:9876。注意 slave 需 conf 显式 brokerRole=SLAVE 才会
// 以 BID=1 注册——仅配 brokerId=1 时两个 broker 会以 BID=0 互相覆盖注册，
// clusterList 只剩一行（本夹具的「未注册」用例正来自该实测现场）。
const rmqRaw = `__IO_BEGIN__
__IO_COMPOSE_BEGIN__
rmq-namesrv-rmq-cluster=running
rmq-broker-broker-a-0=running
__IO_COMPOSE_END__
__IO_CTR__rmq-broker-broker-a-0__=running|1||apache/rocketmq:4.9.7|2026-09-15T18:12:07Z
__IO_CTR__rmq-namesrv-rmq-cluster__=running|1||apache/rocketmq:4.9.7|2026-09-15T18:12:06Z
__IO_RMQ_NS__=172.31.98.10:9876
__IO_RMQ_NSOK__=1
__IO_RMQ_CL_BEGIN__
#Cluster Name     #Broker Name            #BID  #Addr                  #Version                #InTPS(LOAD)       #OutTPS(LOAD) #PCWait(ms) #Hour #SPACE
rmq-cluster       broker-a                0     172.31.98.11:10911     V4_9_7                   0.00(0,0ms)         0.00(0,0ms)          0 497074.35 0.0100
rmq-cluster       broker-a                1     172.31.98.12:10911     V4_9_7                   0.00(0,0ms)         0.00(0,0ms)          0 497074.35 0.0100
__IO_RMQ_CL_END__
__IO_RMQ_BS_BEGIN__
EndTransactionQueueSize         : 0
brokerVersionDesc               : V4_9_7
commitLogDirCapacity            : Total : 1006.9 GiB, Free : 946.8 GiB.
commitLogDiskRatio              : 0.01
__IO_RMQ_BS_END__
__IO_END__
`

// slaveRaw 由 master 夹具派生：本机为 slave（broker-a-1）、无 namesrv 容器。
func slaveRaw() string {
	raw := strings.ReplaceAll(rmqRaw, "rmq-broker-broker-a-0", "rmq-broker-broker-a-1")
	raw = strings.Replace(raw, "rmq-namesrv-rmq-cluster=running\n", "", 1)
	raw = strings.Replace(raw, "__IO_CTR__rmq-namesrv-rmq-cluster__=running|1||apache/rocketmq:4.9.7|2026-09-15T18:12:06Z\n", "", 1)
	return raw
}

func probeCtx(role, ip string) stackkit.ProbeCtx {
	return stackkit.ProbeCtx{
		Mode: "cluster", Role: role, HostIP: ip,
		Params: map[string]string{"broker_port": "10911", "ns_port": "9876"},
	}
}

func findCheck(checks []stackkit.Check, name string) *stackkit.Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

// 容器名含运行期变量（cluster_name / broker_name / broker_id）⇒ Containers 必须为空。
func TestContainersIsEmpty(t *testing.T) {
	if got := New().Containers(probeCtx("master", "172.31.98.11")); len(got) != 0 {
		t.Fatalf("容器名不可预知，应返回空: %v", got)
	}
}

// ScriptTail 契约：现取容器名、复用 inspect_ctr、mqadmin 必须走 $ROCKETMQ_HOME
// （镜像内 /home/rocketmq/bin 不存在——曾令 node.sh 自校验静默失效的实测坑）。
func TestScriptTail(t *testing.T) {
	tail := New().ScriptTail(probeCtx("worker", "172.31.98.12"))
	for _, want := range []string{
		"docker ps", "inspect_ctr", "grep -E '^rmq-(namesrv|broker)-'",
		"namesrvAddr", "__IO_RMQ_NS__=", "__IO_RMQ_NSOK__=",
		"clusterList", "brokerStatus", "$ROCKETMQ_HOME/bin/mqadmin",
		"__IO_RMQ_CL_BEGIN__", "__IO_RMQ_BS_BEGIN__", "_RMQ_SELF='172.31.98.12'",
	} {
		if !strings.Contains(tail, want) {
			t.Fatalf("tail 缺 %q:\n%s", want, tail)
		}
	}
	if strings.Contains(tail, "/home/rocketmq/bin/mqadmin") {
		t.Fatalf("不得使用镜像内不存在的路径 /home/rocketmq/bin/mqadmin:\n%s", tail)
	}
}

// master 机探活：实例×2 + 版本 + 名称服务 + 注册（BID=0）+ 状态（磁盘）。
func TestParseSuiteMaster(t *testing.T) {
	out := New().ParseSuite(probeCtx("master", "172.31.98.11"), rmqRaw, nil)
	if len(out.Checks) != 6 {
		t.Fatalf("期望 6 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	inst := findCheck(out.Checks, "实例")
	if inst == nil || !inst.OK || inst.Detail != "rmq-broker-broker-a-0 running" {
		t.Fatalf("实例检查应只见 broker: %+v", out.Checks)
	}
	if ver := findCheck(out.Checks, "版本"); ver == nil || ver.Detail != "V4_9_7" {
		t.Fatalf("版本应取 brokerStatus 的 brokerVersionDesc: %+v", out.Checks)
	}
	ns := findCheck(out.Checks, "名称服务")
	if ns == nil || !ns.OK || !strings.Contains(ns.Detail, "本机 NameServer") || !strings.Contains(ns.Detail, "单点") {
		t.Fatalf("名称服务应显式报出本机单点 NameServer: %+v", ns)
	}
	reg := findCheck(out.Checks, "注册")
	if reg == nil || !reg.OK || reg.Detail != "172.31.98.11:10911 · BID=0（master）" {
		t.Fatalf("注册检查: %+v", reg)
	}
	st := findCheck(out.Checks, "状态")
	if st == nil || !st.OK || st.Detail != "commitlog 磁盘 Free 946.8 GiB / Total 1006.9 GiB" {
		t.Fatalf("状态检查: %+v", st)
	}
	if out.LiveRole != "master" || !out.LiveRoleFromOutput {
		t.Fatalf("实测角色应为 master: %+v", out)
	}
	if !stackkit.ChecksOK(out.Checks) {
		t.Fatal("健康 master 机应整体通过")
	}
}

// slave 机探活：无 namesrv 容器 ⇒ 名称服务走可达性（可达到 master 的单点）；注册 BID=1。
func TestParseSuiteSlave(t *testing.T) {
	out := New().ParseSuite(probeCtx("worker", "172.31.98.12"), slaveRaw(), nil)
	if len(out.Checks) != 5 {
		t.Fatalf("期望 5 项检查，得到 %d: %+v", len(out.Checks), out.Checks)
	}
	ns := findCheck(out.Checks, "名称服务")
	if ns == nil || !ns.OK || !strings.Contains(ns.Detail, "172.31.98.10:9876 可达") || !strings.Contains(ns.Detail, "单点") {
		t.Fatalf("worker 名称服务应报可达性与单点: %+v", ns)
	}
	reg := findCheck(out.Checks, "注册")
	if reg == nil || !reg.OK || reg.Detail != "172.31.98.12:10911 · BID=1（worker）" {
		t.Fatalf("注册检查: %+v", reg)
	}
	if out.LiveRole != "worker" || !out.LiveRoleFromOutput {
		t.Fatalf("实测角色应为 worker: %+v", out)
	}
	if !stackkit.ChecksOK(out.Checks) {
		t.Fatal("健康 slave 机应整体通过")
	}
}

// 「未注册」现场：两个 brokerId=0 互相覆盖注册后，clusterList 只剩另一台的地址。
func TestParseSuiteNotRegistered(t *testing.T) {
	raw := strings.Replace(slaveRaw(),
		"rmq-cluster       broker-a                1     172.31.98.12:10911     V4_9_7                   0.00(0,0ms)         0.00(0,0ms)          0 497074.35 0.0100\n",
		"", 1)
	out := New().ParseSuite(probeCtx("worker", "172.31.98.12"), raw, nil)
	reg := findCheck(out.Checks, "注册")
	if reg == nil || reg.OK {
		t.Fatalf("覆盖注册时本机地址消失，必须判失败: %+v", reg)
	}
	if !strings.Contains(reg.Detail, "未出现在 clusterList") {
		t.Fatalf("明细应写明未注册: %q", reg.Detail)
	}
	if out.LiveRoleFromOutput {
		t.Fatal("未注册时无实测角色，不应声明 LiveRoleFromOutput")
	}
	if stackkit.ChecksOK(out.Checks) {
		t.Fatal("ChecksOK 应给出本机不通过")
	}
}

// NameServer 端口不可达（NSOK=0）⇒ 名称服务判失败。
func TestParseSuiteNamesrvUnreachable(t *testing.T) {
	raw := strings.Replace(slaveRaw(), "__IO_RMQ_NSOK__=1", "__IO_RMQ_NSOK__=0", 1)
	out := New().ParseSuite(probeCtx("worker", "172.31.98.12"), raw, nil)
	ns := findCheck(out.Checks, "名称服务")
	if ns == nil || ns.OK || !strings.Contains(ns.Detail, "不可达") {
		t.Fatalf("不可达应判失败: %+v", ns)
	}
}

// master 机未发现 NameServer 容器（本应部署）⇒ 名称服务判失败并写明原因。
func TestParseSuiteMasterWithoutNamesrv(t *testing.T) {
	raw := strings.Replace(rmqRaw, "rmq-namesrv-rmq-cluster=running\n", "", 1)
	raw = strings.Replace(raw, "__IO_CTR__rmq-namesrv-rmq-cluster__=running|1||apache/rocketmq:4.9.7|2026-09-15T18:12:06Z\n", "", 1)
	raw = strings.Replace(raw, "__IO_RMQ_NSOK__=1", "__IO_RMQ_NSOK__=0", 1)
	out := New().ParseSuite(probeCtx("master", "172.31.98.11"), raw, nil)
	ns := findCheck(out.Checks, "名称服务")
	if ns == nil || ns.OK || !strings.Contains(ns.Detail, "未发现 NameServer 容器") {
		t.Fatalf("master 缺 NameServer 应判失败: %+v", ns)
	}
}

// brokerStatus 无响应 ⇒ 状态判失败、版本回落镜像；注册不受影响。
func TestParseSuiteBrokerStatusDown(t *testing.T) {
	raw := strings.Replace(rmqRaw,
		"EndTransactionQueueSize         : 0\nbrokerVersionDesc               : V4_9_7\ncommitLogDirCapacity            : Total : 1006.9 GiB, Free : 946.8 GiB.\ncommitLogDiskRatio              : 0.01\n",
		"", 1)
	out := New().ParseSuite(probeCtx("master", "172.31.98.11"), raw, nil)
	st := findCheck(out.Checks, "状态")
	if st == nil || st.OK || !strings.Contains(st.Detail, "无响应") {
		t.Fatalf("无响应应判失败: %+v", st)
	}
	if ver := findCheck(out.Checks, "版本"); ver == nil || ver.Detail != "apache/rocketmq:4.9.7" {
		t.Fatalf("版本应回落镜像: %+v", out.Checks)
	}
	if reg := findCheck(out.Checks, "注册"); reg == nil || !reg.OK {
		t.Fatalf("注册不应受 brokerStatus 影响: %+v", reg)
	}
}

// 无任何容器：套件专属检查项为零（引擎的 compose 兜底负责「容器」红灯）。
func TestParseSuiteNoContainers(t *testing.T) {
	out := New().ParseSuite(probeCtx("master", "172.31.98.11"), "__IO_BEGIN__\n__IO_END__\n", nil)
	if len(out.Checks) != 0 {
		t.Fatalf("无容器时不应有检查项: %+v", out.Checks)
	}
}

// clusterList 解析：跳过表头与错误行（字段数够但第 3 列非数字）。
func TestParseClusterList(t *testing.T) {
	block := "#Cluster Name     #Broker Name            #BID  #Addr                  #Version\n" +
		"connector failed: connect to null failed\n" +
		"rmq-cluster       broker-a                0     172.31.98.11:10911     V4_9_7\n" +
		"rmq-cluster       broker-a                1     172.31.98.12:10911     V4_9_7\n"
	rows := parseClusterList(block)
	if len(rows) != 2 {
		t.Fatalf("应解析出 2 行: %+v", rows)
	}
	if rows[0].bid != 0 || rows[0].addr != "172.31.98.11:10911" || rows[1].bid != 1 {
		t.Fatalf("行解析: %+v", rows)
	}
}

// 磁盘容量摘要：匹配实测值、无匹配回落空。
func TestDiskSummary(t *testing.T) {
	if got := rmqDiskSummary("Total : 1006.9 GiB, Free : 946.8 GiB."); got != "Free 946.8 GiB / Total 1006.9 GiB" {
		t.Fatalf("磁盘摘要: %q", got)
	}
	if got := rmqDiskSummary(""); got != "" {
		t.Fatalf("无值应回落空: %q", got)
	}
}

// 端点：master 机 NameServer + Broker 两条，worker 机仅 Broker；端口取参数。
func TestEndpoints(t *testing.T) {
	d := New()
	master := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.3.1", Role: "master"},
	})
	if len(master) != 2 {
		t.Fatalf("master 应含 NameServer + Broker: %+v", master)
	}
	if master[0].URL != "rocketmq://10.0.3.1:9876" || master[0].Component != "namesrv" || master[0].Role != "master" {
		t.Fatalf("NameServer 端点: %+v", master[0])
	}
	if master[1].URL != "rocketmq://10.0.3.1:10911" || master[1].Component != "broker" {
		t.Fatalf("Broker 端点: %+v", master[1])
	}
	worker := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.3.2", Role: "worker"},
	})
	if len(worker) != 1 || worker[0].URL != "rocketmq://10.0.3.2:10911" || worker[0].Role != "worker" {
		t.Fatalf("worker 只应含 Broker: %+v", worker)
	}
	custom := d.Endpoints(stackkit.EndpointCtx{
		Mode: "cluster", Host: model.StackInstanceHost{HostIP: "10.0.3.1", Role: "master"},
		Params: map[string]string{"ns_port": "19876", "broker_port": "20911"},
	})
	if custom[0].URL != "rocketmq://10.0.3.1:19876" || custom[1].URL != "rocketmq://10.0.3.1:20911" {
		t.Fatalf("自定义端口: %+v", custom)
	}
}
