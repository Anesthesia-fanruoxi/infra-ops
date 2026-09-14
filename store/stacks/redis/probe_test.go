package redis

import (
	"strings"
	"testing"

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
