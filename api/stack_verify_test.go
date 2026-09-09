package api

import (
	"strings"
	"testing"

	"infra-ops/model"
)

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

func TestParseBigdataCtrChecks(t *testing.T) {
	raw := `__IO_CTR__hadoop-namenode__=running|1||apache/hadoop:3.3.6|2026-09-09T01:00:00Z
__IO_CTR__spark-master__=running|1||bitnami/spark:3.5|2026-09-09T01:00:00Z
`
	row := stackVerifyHost{}
	ok := parseBigdataCtrChecks([]string{"hadoop-namenode", "spark-master"}, raw, &row)
	if !ok {
		t.Fatalf("want ok, checks=%+v", row.Checks)
	}
	inst := ""
	for _, c := range row.Checks {
		if c.Name == "实例" {
			inst = c.Detail
		}
	}
	if !strings.Contains(inst, "hadoop-namenode") || !strings.Contains(inst, "spark-master") {
		t.Fatalf("inst=%s", inst)
	}

	row2 := stackVerifyHost{}
	ok = parseBigdataCtrChecks([]string{"hadoop-namenode"}, `__IO_CTR__hadoop-namenode__=missing`, &row2)
	if ok {
		t.Fatalf("missing should fail")
	}
}

func TestBigdataExpectedContainers(t *testing.T) {
	params := map[string]string{"components": "hdfs,spark,hive", "masters": `{"hdfs":"10.0.0.1","spark":"10.0.0.1","hive":"10.0.0.1"}`}
	master := bigdataExpectedContainers("10.0.0.1", "master", params)
	want := []string{"hadoop-namenode", "spark-master", "hive-metastore", "hive-hiveserver2"}
	if strings.Join(master, ",") != strings.Join(want, ",") {
		t.Fatalf("master=%v", master)
	}
	worker := bigdataExpectedContainers("10.0.0.2", "worker", params)
	wantW := []string{"hadoop-datanode", "spark-worker"}
	if strings.Join(worker, ",") != strings.Join(wantW, ",") {
		t.Fatalf("worker=%v", worker)
	}
}

func TestBigdataExpectedContainersHA(t *testing.T) {
	// 全家桶 HA：主=.1，NN2/RM2/M2/JM2/HM2/HS2 备=.2，JN/ZK ensemble=.1,.2,.3，MS2=.2，metastore_db=.3
	ha := func() map[string]string {
		return map[string]string{
			"components": "hdfs,zookeeper,yarn,spark,flink,hive,metastore_db,hbase,trino",
			"ha":         "true",
			"masters": `{"hdfs":"10.0.0.1","yarn":"10.0.0.1","spark":"10.0.0.1","flink":"10.0.0.1","hive":"10.0.0.1","hbase":"10.0.0.1","trino":"10.0.0.1",` +
				`"hdfs_nn2":"10.0.0.2","yarn_rm2":"10.0.0.2","spark_m2":"10.0.0.2","flink_jm2":"10.0.0.2","hbase_hm2":"10.0.0.2",` +
				`"hive_ms2":"10.0.0.2","hive_hs2b":"10.0.0.2","hive_db":"10.0.0.3","hdfs_jns":"10.0.0.1,10.0.0.2,10.0.0.3"}`,
		}
	}
	primary := bigdataExpectedContainers("10.0.0.1", "master", ha())
	// 主节点：NN1+zkfc、RM1、Master、JM、MS1+HS1、HMaster、ZK、Trino（NN1/NN2 主机不承载 journalnode，仅纯 JN 主机运行）
	primaryW := []string{"bigdata-zookeeper", "hadoop-namenode", "hadoop-zkfc", "hadoop-resourcemanager",
		"spark-master", "flink-jobmanager", "hbase-master", "hive-metastore", "hive-hiveserver2", "trino-node"}
	if strings.Join(primary, ",") != strings.Join(primaryW, ",") {
		t.Fatalf("primary=%v", primary)
	}
	secondary := bigdataExpectedContainers("10.0.0.2", "node", ha())
	secW := []string{"bigdata-zookeeper", "hadoop-namenode2", "hadoop-zkfc2", "hadoop-resourcemanager2",
		"spark-master2", "flink-jobmanager2", "hbase-backup-master", "hive-metastore2", "hive-hiveserver2-2", "trino-node"}
	if strings.Join(secondary, ",") != strings.Join(secW, ",") {
		t.Fatalf("secondary=%v", secondary)
	}
	// 第三台：纯 JN + ZK + metastore_db + 其余工作节点容器（JN 主机不跑 datanode）
	w3 := bigdataExpectedContainers("10.0.0.3", "node", ha())
	w3W := []string{"bigdata-zookeeper", "hadoop-journalnode", "hadoop-nodemanager",
		"spark-worker", "flink-taskmanager", "hbase-regionserver", "hive-metastore-db", "trino-node"}
	if strings.Join(w3, ",") != strings.Join(w3W, ",") {
		t.Fatalf("w3=%v", w3)
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
