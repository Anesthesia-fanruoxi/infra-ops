package stack

import (
	"strings"
	"testing"

	"infra-ops/model"
)

func TestParseCtrState(t *testing.T) {
	ok, d := parseCtrState("redis-repl", "running|1|healthy")
	if !ok || !strings.Contains(d, "healthy") {
		t.Fatalf("%v %s", ok, d)
	}
	ok, _ = parseCtrState("redis-repl", "exited|0|")
	if ok {
		t.Fatal("exited should fail")
	}
	ok, _ = parseCtrState("redis-repl", "running|1|unhealthy")
	if ok {
		t.Fatal("unhealthy should fail")
	}
	ok, _ = parseCtrState("redis-repl", "missing")
	if ok {
		t.Fatal("missing should fail")
	}
}

// 下列三个用例走的是引擎分发路径：套件（redis）实现 ProbePlugin 后，
// 容器/PING/角色/复制/集群等检查项由 store/stacks/redis 产出，引擎只做落库与判定。
func TestParseStackVerifyOutputRedisMaster(t *testing.T) {
	raw := `__IO_BEGIN__
__IO_CTR__redis-repl__=running|1|healthy
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
__IO_END__
`
	row := stackVerifyHost{}
	parseStackVerifyOutput("redis", "replication", model.StackInstanceHost{Role: "master"}, nil, raw, &row)
	if !row.OK {
		t.Fatalf("want ok, checks=%v", row.Checks)
	}
	if row.LiveRole != "master" {
		t.Fatalf("live=%s", row.LiveRole)
	}
}

func TestParseStackVerifyOutputReplicaLinkDown(t *testing.T) {
	raw := `__IO_CTR__redis-repl__=running|1|
__IO_PING__=PONG
__IO_REPL_BEGIN__
role:slave
master_host:10.0.0.1
master_link_status:down
__IO_REPL_END__
`
	row := stackVerifyHost{}
	parseStackVerifyOutput("redis", "replication", model.StackInstanceHost{Role: "replica"}, nil, raw, &row)
	if row.OK {
		t.Fatalf("link down should fail: %+v", row.Checks)
	}
}

func TestParseStackVerifyOutputCluster(t *testing.T) {
	raw := `__IO_CTR__redis-cluster__=running|1|healthy
__IO_PING__=PONG
__IO_REPL_BEGIN__
role:master
connected_slaves:0
__IO_REPL_END__
__IO_CLUSTER_BEGIN__
cluster_state:ok
cluster_slots_assigned:16384
cluster_known_nodes:6
__IO_CLUSTER_END__
`
	row := stackVerifyHost{}
	parseStackVerifyOutput("redis", "cluster", model.StackInstanceHost{Role: "node"}, nil, raw, &row)
	if !row.OK {
		t.Fatalf("%+v", row.Checks)
	}
}

// 未实现 ProbePlugin 的套件（如 kafka）走通用 compose 兜底。
func TestParseStackVerifyOutputComposeFallback(t *testing.T) {
	raw := `__IO_COMPOSE_BEGIN__
kafka=running
__IO_COMPOSE_END__
`
	row := stackVerifyHost{}
	parseStackVerifyOutput("kafka", "kraft", model.StackInstanceHost{Role: "node"}, nil, raw, &row)
	if !row.OK {
		t.Fatalf("compose running should pass: %+v", row.Checks)
	}
	if row.LiveRole != "node" {
		t.Fatalf("live=%s", row.LiveRole)
	}
}

func TestAssembleRedisReplicationTopology(t *testing.T) {
	inst := &model.StackInstance{StackKey: "redis", Mode: "replication", StackName: "Redis", ParamsJSON: `{"port":"6379"}`}
	hosts := []stackVerifyHost{
		{HostIP: "10.0.0.1", Role: "master", LiveRole: "master", OK: true},
		{HostIP: "10.0.0.2", Role: "replica", LiveRole: "slave", OK: true},
	}
	res := assembleStackVerify(inst, map[string]string{"port": "6379"}, hosts)
	if !res.OK {
		t.Fatalf("summary=%s notes=%v", res.Summary, res.Notes)
	}
	if !strings.Contains(strings.Join(res.Notes, ","), "主从复制正常") {
		t.Fatalf("%v", res.Notes)
	}
	hosts[0].LiveRole = "slave"
	hosts[1].LiveRole = "slave"
	res = assembleStackVerify(inst, map[string]string{"port": "6379"}, hosts)
	if res.OK {
		t.Fatalf("two slaves should fail: %v", res.Notes)
	}
}

func TestBashQuote(t *testing.T) {
	if bashQuote(`a'b`) != `'a'"'"'b'` {
		t.Fatalf("%s", bashQuote(`a'b`))
	}
}

func TestRedactSecret(t *testing.T) {
	if got := redactSecret("auth SuperSecret", "SuperSecret"); got != "auth ***" {
		t.Fatalf("%s", got)
	}
}

func TestExtractBlock(t *testing.T) {
	s := "a\n__IO_REPL_BEGIN__\nrole:master\n__IO_REPL_END__\nb"
	if got := extractBlock(s, "__IO_REPL_BEGIN__", "__IO_REPL_END__"); got != "role:master" {
		t.Fatalf("%q", got)
	}
}
