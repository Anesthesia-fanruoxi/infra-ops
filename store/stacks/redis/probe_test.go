package redis

import (
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Redis INFO / SENTINEL / 容器解析单测（原 api/stack/stack_verify_test.go 的 redis 用例随实现迁入）。

func TestParseRedisInfoReplication(t *testing.T) {
	raw := `# Replication
role:master
connected_slaves:1
slave0:ip=10.0.0.2,port=6379,state=online,offset=123,lag=0
master_failover_state:no-failover
`
	info := parseRedisInfo(raw)
	if info["role"] != "master" || info["connected_slaves"] != "1" {
		t.Fatalf("%v", info)
	}
	slaves := parseConnectedSlaves(info)
	if len(slaves) != 1 || !strings.Contains(slaves[0], "10.0.0.2") || !strings.Contains(slaves[0], "online") {
		t.Fatalf("%v", slaves)
	}
}

func TestParseClusterInfo(t *testing.T) {
	raw := `cluster_state:ok
cluster_slots_assigned:16384
cluster_slots_ok:16384
cluster_known_nodes:6
cluster_size:3
`
	info := parseRedisInfo(raw)
	if info["cluster_state"] != "ok" || info["cluster_slots_assigned"] != "16384" {
		t.Fatalf("%v", info)
	}
}

func TestParseSentinelMasterRaw(t *testing.T) {
	raw := `name
mymaster
ip
10.1.1.8
port
6379
flags
master
num-slaves
2
num-other-sentinels
2
`
	m := parseSentinelMaster(raw)
	if m["ip"] != "10.1.1.8" || m["flags"] != "master" || m["num-slaves"] != "2" {
		t.Fatalf("%v", m)
	}
}

func TestParseSentinelMasterPretty(t *testing.T) {
	raw := ` 1) "name"
 2) "mymaster"
 3) "ip"
 4) "10.1.1.8"
 5) "flags"
 6) "master"
`
	m := parseSentinelMaster(raw)
	if m["name"] != "mymaster" || m["ip"] != "10.1.1.8" || m["flags"] != "master" {
		t.Fatalf("%v", m)
	}
}

// 期望容器按模式切换（探活脚本 inspect 的容器名）。
func TestContainersByMode(t *testing.T) {
	d := New()
	cases := map[string][]string{
		"replication": {"redis-repl"},
		"cluster":     {"redis-cluster"},
		"sentinel":    {"redis-sentinel-data", "redis-sentinel"},
	}
	for mode, want := range cases {
		got := d.Containers(stackkit.ProbeCtx{Mode: mode})
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v want %v", mode, got, want)
		}
	}
}

// ParseSuite 输出形态：LiveRole 来自实测、OK 不显式覆盖（由引擎按检查项判定）。
func TestParseSuiteReplicationMaster(t *testing.T) {
	raw := `__IO_CTR__redis-repl__=running|1|healthy
__IO_PING__=PONG
__IO_REPL_BEGIN__
role:master
connected_slaves:1
slave0:ip=10.0.0.2,port=6379,state=online,offset=1,lag=0
__IO_REPL_END__
__IO_SRV_BEGIN__
redis_version:7.4.2
uptime_in_days:0
__IO_SRV_END__
`
	out := New().ParseSuite(stackkit.ProbeCtx{Mode: "replication", Role: "master"}, raw, []string{"redis-repl"})
	if out.OK != nil {
		t.Fatalf("redis 不应显式覆盖 OK: %v", *out.OK)
	}
	if !out.LiveRoleFromOutput || out.LiveRole != "master" {
		t.Fatalf("live=%q owned=%v", out.LiveRole, out.LiveRoleFromOutput)
	}
	if !stackkit.ChecksOK(out.Checks) {
		t.Fatalf("checks=%+v", out.Checks)
	}
	names := make([]string, 0, len(out.Checks))
	for _, c := range out.Checks {
		names = append(names, c.Name)
	}
	for _, want := range []string{"容器 redis-repl", "PING", "角色", "复制", "版本"} {
		if !contains(names, want) {
			t.Errorf("缺少检查项 %s：%v", want, names)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// 组件归属（探活对话框的页签来源）：哨兵模式下 Redis 实体与 Sentinel 各自归位，
// 容器名含 sentinel 的 redis-sentinel-data 属于 Redis 本体，不能按子串误判。
func TestParseSuiteSentinelComponents(t *testing.T) {
	raw := `__IO_CTR__redis-sentinel-data__=running|1|healthy
__IO_CTR__redis-sentinel__=running|1|healthy
__IO_PING__=PONG
__IO_REPL_BEGIN__
role:master
__IO_REPL_END__
__IO_SENT_PING__=PONG
__IO_SENT_BEGIN__
name
mymaster
ip
10.0.0.1
port
6379
flags
master
__IO_SENT_END__
`
	out := New().ParseSuite(stackkit.ProbeCtx{Mode: "sentinel", Role: "master"}, raw,
		[]string{"redis-sentinel-data", "redis-sentinel"})
	want := map[string]string{
		"容器 redis-sentinel-data": "redis",
		"容器 redis-sentinel":      "sentinel",
		"PING":                     "redis",
		"角色":                      "redis",
		"哨兵":                      "sentinel",
	}
	for name, comp := range want {
		found := false
		for _, c := range out.Checks {
			if c.Name != name {
				continue
			}
			found = true
			if c.Component != comp {
				t.Errorf("%s component=%q want %q", name, c.Component, comp)
			}
		}
		if !found {
			t.Errorf("缺少检查项 %s", name)
		}
	}
}

// 端点归属：redis:// 归 Redis 本体、redis-sentinel:// 归 Sentinel。
func TestEndpointsComponent(t *testing.T) {
	eps := New().Endpoints(stackkit.EndpointCtx{
		Mode:   "sentinel",
		Host:   model.StackInstanceHost{HostIP: "10.0.0.1", Role: "master"},
		Params: map[string]string{"sentinel_port": "26379"},
	})
	if len(eps) != 2 {
		t.Fatalf("endpoints=%+v", eps)
	}
	if eps[0].Component != "redis" || eps[1].Component != "sentinel" {
		t.Fatalf("component 归属错误: %+v", eps)
	}
	if eps[1].URL != "redis-sentinel://10.0.0.1:26379" {
		t.Fatalf("哨兵端点=%q", eps[1].URL)
	}
}

// 哨兵主名参数化：探活脚本与接入提示都跟随 master_name（默认值仍为 mymaster）。
func TestSentinelMasterNameParam(t *testing.T) {
	d := New()
	tail := d.ScriptTail(stackkit.ProbeCtx{Mode: "sentinel", Params: map[string]string{"master_name": "biz-redis"}})
	if !strings.Contains(tail, `SENT_MASTER='biz-redis'`) || !strings.Contains(tail, `SENTINEL master "$SENT_MASTER"`) {
		t.Fatalf("脚本尾片段未参数化:\n%s", tail)
	}
	def := d.ScriptTail(stackkit.ProbeCtx{Mode: "sentinel", Params: map[string]string{}})
	if !strings.Contains(def, `SENT_MASTER='mymaster'`) {
		t.Fatalf("默认主名应为 mymaster:\n%s", def)
	}
	_, hint := d.Summary(stackkit.ProbeCtx{Mode: "sentinel", Params: map[string]string{"master_name": "biz-redis"}},
		[]stackkit.ProbeHostRow{{HostIP: "10.0.0.1", Role: "master"}})
	if !strings.Contains(hint, "get-master-addr-by-name biz-redis") {
		t.Fatalf("接入提示未跟随主名: %s", hint)
	}
}
