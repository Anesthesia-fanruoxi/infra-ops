package stack

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

func TestMergeStackParamsHomeDir(t *testing.T) {
	bp := store.FindBuiltinStack("redis").Blueprint()
	got, err := mergeStackParams(bp, "replication", map[string]string{"password": "p"}, nil, "/data/redis-repl")
	if err != nil {
		t.Fatal(err)
	}
	if got["home_dir"] != "/data/redis-repl" {
		t.Fatalf("home_dir=%s", got["home_dir"])
	}
	if got["port"] != "6379" {
		t.Fatalf("port=%s", got["port"])
	}
	got, err = mergeStackParams(bp, "cluster", map[string]string{"password": "p", "replicas": "1"},
		map[string]string{"port": "6380"}, "/data/redis-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if got["home_dir"] != "/data/redis-cluster" || got["port"] != "6380" || got["replicas"] != "1" {
		t.Fatalf("%v", got)
	}
	got, err = mergeStackParams(bp, "sentinel", map[string]string{"password": "p"}, nil, "/data/redis-sentinel")
	if err != nil {
		t.Fatal(err)
	}
	if got["home_dir"] != "/data/redis-sentinel" || got["sentinel_port"] != "26379" {
		t.Fatalf("%v", got)
	}
}

func TestMergeStackParamsNewStacks(t *testing.T) {
	bd := store.FindBuiltinStack("bigdata").Blueprint()
	got, err := mergeStackParams(bd, "cluster", map[string]string{"components": "hdfs,spark"}, nil, "/data/bigdata")
	if err != nil {
		t.Fatal(err)
	}
	if got["home_dir"] != "/data/bigdata" || got["nn_rpc_port"] != "9000" || got["image_spark"] != "apache/spark:3.5.1" {
		t.Fatalf("%v", got)
	}
	kf := store.FindBuiltinStack("kafka").Blueprint()
	got, err = mergeStackParams(kf, "kraft", nil, nil, "/data/kafka-kraft")
	if err != nil {
		t.Fatal(err)
	}
	if got["home_dir"] != "/data/kafka-kraft" || got["port"] != "9092" {
		t.Fatalf("%v", got)
	}
	es := store.FindBuiltinStack("elasticsearch").Blueprint()
	got, err = mergeStackParams(es, "cluster", nil, nil, "/data/elasticsearch")
	if err != nil {
		t.Fatal(err)
	}
	if got["port"] != "9200" || got["transport_port"] != "9300" {
		t.Fatalf("%v", got)
	}
	rmq := store.FindBuiltinStack("rocketmq").Blueprint()
	got, err = mergeStackParams(rmq, "cluster", map[string]string{"cluster_name": "c", "broker_name": "b", "broker_port": "10911"},
		map[string]string{"home_dir": "/data/rocketmq", "ns_port": "9876"}, "/data/rocketmq") // 一步成型：cluster 单模式
	if err != nil {
		t.Fatal(err)
	}
	if got["ns_port"] != "9876" || got["broker_port"] != "10911" || got["cluster_name"] != "c" {
		t.Fatalf("%v", got)
	}
	nc := store.FindBuiltinStack("nacos").Blueprint()
	got, err = mergeStackParams(nc, "cluster", map[string]string{"db_password": "p"}, nil, "/data/nacos")
	if err != nil {
		t.Fatal(err)
	}
	if got["port"] != "8848" || got["mysql_image"] != "mysql:8.0" || got["db_host"] != "" {
		t.Fatalf("%v", got)
	}
	got, err = mergeStackParams(nc, "cluster", map[string]string{"db_password": "p", "db_host": "10.0.0.9:3307"}, nil, "/data/nacos")
	if err != nil {
		t.Fatal(err)
	}
	if got["db_host"] != "10.0.0.9:3307" {
		t.Fatalf("%v", got)
	}
	pj := store.FindBuiltinStack("powerjob").Blueprint()
	got, err = mergeStackParams(pj, "cluster", map[string]string{"db_password": "p"}, nil, "/data/powerjob")
	if err != nil {
		t.Fatal(err)
	}
	if got["server_port"] != "7700" || got["akka_port"] != "10086" || got["db_host"] != "" {
		t.Fatalf("%v", got)
	}
}

func TestStackClusterExtraMergedScaleOut(t *testing.T) {
	existing := []model.StackRunHost{
		{HostID: 1, HostIP: "10.0.0.1", Role: "master", Seq: 1, ParamsJSON: `{"port":"9200","transport_port":"9300"}`},
		{HostID: 2, HostIP: "10.0.0.2", Role: "worker", Seq: 2, ParamsJSON: `{"port":"9200","transport_port":"9300"}`},
	}
	newbie := []model.StackRunHost{
		{HostID: 3, HostIP: "10.0.0.3", Role: "worker", Seq: 3, ParamsJSON: `{"port":"9200","transport_port":"9300"}`},
	}
	extra := stackkit.ClusterExtraMerged("scale_out", existing, newbie)
	got := extra[3]
	if got["__is_bootstrap"] != "false" {
		t.Fatalf("new node should not bootstrap: %v", got)
	}
	if got["__master_ip"] != "10.0.0.1" {
		t.Fatalf("master_ip=%s", got["__master_ip"])
	}
	if !strings.Contains(got["__seed_hosts"], "10.0.0.1:9300") {
		t.Fatalf("seed_hosts=%s", got["__seed_hosts"])
	}
	if got["__new_nodes"] != "10.0.0.3:9200" {
		t.Fatalf("new_nodes=%s", got["__new_nodes"])
	}
	if extra[1]["__is_bootstrap"] != "true" {
		t.Fatalf("existing master still bootstrap: %v", extra[1])
	}
}

func TestValidateScaleIn(t *testing.T) {
	bp := store.FindBuiltinStack("elasticsearch")
	inst := &model.StackInstance{Mode: "cluster", StackKey: "elasticsearch"}
	active := []model.StackInstanceHost{
		{HostID: 1, Role: "master"},
		{HostID: 2, Role: "worker"},
	}
	if err := validateScaleIn(bp, inst, active, []int64{1, 2}); err == nil {
		t.Fatal("expected refuse remove all")
	}
	if err := validateScaleIn(bp, inst, active, []int64{2}); err != nil {
		t.Fatal(err)
	}
	redis := store.FindBuiltinStack("redis")
	rinst := &model.StackInstance{Mode: "replication", StackKey: "redis"}
	if err := validateScaleIn(redis, rinst, active, []int64{1}); err == nil {
		t.Fatal("expected refuse unique master")
	}
}

func TestValidateScaleInBigdataHA(t *testing.T) {
	bp := store.FindBuiltinStack("bigdata")
	params, _ := json.Marshal(map[string]string{
		"ha": "true", "components": "hdfs,zookeeper,yarn,hive,metastore_db",
		"masters": `{"hdfs":"10.0.0.1","hive":"10.0.0.1"}`,
	})
	active := []model.StackInstanceHost{
		{HostID: 1, HostIP: "10.0.0.1", Role: "master", Seq: 1, Status: "active", ParamsJSON: string(params)},
		{HostID: 2, HostIP: "10.0.0.2", Role: "worker", Seq: 2, Status: "active", ParamsJSON: string(params)},
		{HostID: 3, HostIP: "10.0.0.3", Role: "worker", Seq: 3, Status: "active", ParamsJSON: string(params)},
	}
	inst := &model.StackInstance{StackKey: "bigdata", Mode: "cluster", ParamsJSON: string(params)}
	// 10.0.0.1 承载 NN1/RM1/SparkM1/JM1/HM1/MS1/MetaDB/ZK 等角色 → 不可缩容
	if err := validateScaleIn(bp, inst, active, []int64{1}); err == nil {
		t.Fatal("expected refuse HA protected primary host")
	}
	// 10.0.0.2 承载 NN2(自动第二台) 角色 → 不可缩容
	if err := validateScaleIn(bp, inst, active, []int64{2}); err == nil {
		t.Fatal("expected refuse HA secondary host (NN2/BROKER)")
	}
	// 非 HA 实例（ha=false）不触发角色保护
	nonHAParams, _ := json.Marshal(map[string]string{"ha": "false"})
	inst2 := &model.StackInstance{StackKey: "bigdata", Mode: "cluster", ParamsJSON: string(nonHAParams)}
	act2 := []model.StackInstanceHost{
		{HostID: 1, HostIP: "10.0.0.1", Role: "master", Seq: 1, Status: "active", ParamsJSON: string(nonHAParams)},
		{HostID: 2, HostIP: "10.0.0.2", Role: "worker", Seq: 2, Status: "active", ParamsJSON: string(nonHAParams)},
	}
	if err := validateScaleIn(bp, inst2, act2, []int64{2}); err != nil {
		t.Fatalf("非 HA 缩容 worker 应成功: %v", err)
	}
}

func TestReplayStackMemberPlan(t *testing.T) {
	h := func(id int64, status string) model.StackRunHost {
		return model.StackRunHost{HostID: id, HostIP: fmt.Sprintf("10.0.0.%d", id), Status: status}
	}
	got := replayStackMemberPlan([]stackMemberPlanEvent{
		{Op: "create", Hosts: []model.StackRunHost{h(1, "failed"), h(2, "failed")}},
	})
	if len(got) != 2 || got[0].HostID != 1 || got[1].HostID != 2 {
		t.Fatalf("create plan: %+v", got)
	}
	got = replayStackMemberPlan([]stackMemberPlanEvent{
		{Op: "create", Hosts: []model.StackRunHost{h(1, "success"), h(2, "success")}},
		{Op: "scale_out", Hosts: []model.StackRunHost{h(3, "failed")}},
		{Op: "uninstall", Status: "success", Hosts: []model.StackRunHost{h(1, "success"), h(2, "success")}},
	})
	if len(got) != 3 || got[2].HostID != 3 {
		t.Fatalf("scale_out then uninstall should keep members: %+v", got)
	}
	got = replayStackMemberPlan([]stackMemberPlanEvent{
		{Op: "create", Hosts: []model.StackRunHost{h(1, "success"), h(2, "success"), h(3, "success")}},
		{Op: "scale_in", Hosts: []model.StackRunHost{h(3, "success")}},
	})
	if len(got) != 2 || got[0].HostID != 1 || got[1].HostID != 2 {
		t.Fatalf("scale_in plan: %+v", got)
	}
}
