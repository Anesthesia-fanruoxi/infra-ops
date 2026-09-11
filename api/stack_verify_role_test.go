package api

import (
	"encoding/json"
	"strings"
	"testing"

	"infra-ops/model"
)

// 复现实例 9（HA 5 节点）的角色解析：持久化的 masters 里只有 hdfs_nn2/spark_m2/yarn_rm2，
// flink_jm2 / hbase_hm2 / hive_ms2 / hive_hs2b 由引擎按「第一台非主主机」自动落点。
const bigdataHAMasters = `{"flink":"192.168.3.40","hbase":"192.168.3.40","hdfs":"192.168.3.40",` +
	`"hdfs_nn2":"192.168.3.41","hive":"192.168.3.40","spark":"192.168.3.40",` +
	`"spark_m2":"192.168.3.41","yarn":"192.168.3.40","yarn_rm2":"192.168.3.41"}`

func bigdataHAFixture() (*model.StackInstance, map[string]string) {
	instParams := map[string]string{
		"ha":         "true",
		"masters":    bigdataHAMasters,
		"components": "hdfs,zookeeper,yarn,spark,flink,hbase,metastore_db,hive,trino",
	}
	ips := []string{"192.168.3.40", "192.168.3.41", "192.168.3.42", "192.168.3.43", "192.168.3.44"}
	inst := &model.StackInstance{ID: 9, StackKey: "bigdata", Mode: "cluster", Status: "ready"}
	hostParams, _ := json.Marshal(instParams)
	for i, ip := range ips {
		role := "worker"
		if i == 0 {
			role = "master"
		}
		// 与线上一致：主机级 params_json 里也带了一份（缺自动落点键的）masters
		inst.Hosts = append(inst.Hosts, model.StackInstanceHost{
			HostID: int64(i + 1), HostIP: ip, Role: role, Seq: i + 1, Status: "active",
			ParamsJSON: string(hostParams),
		})
	}
	return inst, instParams
}

// 41 是 HA 备主所在主机：应期望 namenode2 / zkfc2 / journalnode / datanode / rm2 /
// spark-master2 / flink-jobmanager2 / hbase-backup-master / hive-metastore2 /
// hive-hiveserver2-2 / trino，而不是 worker 容器。
func TestBigdataExpectedContainers_HASecondaryHost(t *testing.T) {
	inst, instParams := bigdataHAFixture()
	// 主机级参数合并后必须由权威角色矩阵覆盖，否则旧 masters 会把 41 降级成工作节点
	merged := mergeParamMaps(instParams, parseJSONMap(inst.Hosts[1].ParamsJSON))
	overrideBigdataMasters(inst, merged)

	got := bigdataExpectedContainers("192.168.3.41", "worker", merged)
	t.Logf("41 期望容器 = %v", got)

	want := []string{
		"bigdata-zookeeper", "hadoop-namenode2", "hadoop-zkfc2", "hadoop-journalnode",
		"hadoop-datanode", "hadoop-resourcemanager2", "spark-master2", "flink-jobmanager2",
		"hbase-backup-master", "hive-metastore2", "hive-hiveserver2-2", "trino-node",
	}
	for _, w := range want {
		if !containsString(got, w) {
			t.Errorf("41 应期望容器 %s，实际 %v", w, got)
		}
	}
	for _, bad := range []string{"flink-taskmanager", "hbase-regionserver", "hadoop-nodemanager"} {
		if containsString(got, bad) {
			t.Errorf("41 不应期望 worker 容器 %s，实际 %v", bad, got)
		}
	}
}

// 42（普通工作节点 + JN）与 43/44（普通工作节点）不得被误判为备主。
func TestBigdataExpectedContainers_HAWorkerHosts(t *testing.T) {
	inst, instParams := bigdataHAFixture()
	for _, ip := range []string{"192.168.3.42", "192.168.3.43", "192.168.3.44"} {
		merged := mergeParamMaps(instParams, parseJSONMap(inst.Hosts[0].ParamsJSON))
		overrideBigdataMasters(inst, merged)
		got := bigdataExpectedContainers(ip, "worker", merged)
		for _, w := range []string{"hadoop-nodemanager", "spark-worker", "flink-taskmanager", "hbase-regionserver", "hadoop-datanode"} {
			if !containsString(got, w) {
				t.Errorf("%s 应期望容器 %s，实际 %v", ip, w, got)
			}
		}
		if containsString(got, "flink-jobmanager2") || containsString(got, "hbase-backup-master") {
			t.Errorf("%s 不应期望备主容器，实际 %v", ip, got)
		}
	}
	// 42 属于 JNS 前三台，应额外有 journalnode；44 不在 JNS
	merged := mergeParamMaps(instParams, parseJSONMap(inst.Hosts[0].ParamsJSON))
	overrideBigdataMasters(inst, merged)
	if !containsString(bigdataExpectedContainers("192.168.3.42", "worker", merged), "hadoop-journalnode") {
		t.Errorf("42 应在 JNS 内，期望 hadoop-journalnode")
	}
	if containsString(bigdataExpectedContainers("192.168.3.44", "worker", merged), "hadoop-journalnode") {
		t.Errorf("44 不在 JNS 内，不应期望 hadoop-journalnode")
	}
}

// 引擎侧（stackBigdataRole）与探活侧（bigdataRoleMasters）必须得出同一套角色落点。
func TestBigdataRole_MatchesVerifyExpectation(t *testing.T) {
	inst, _ := bigdataHAFixture()
	role := stackBigdataRole(instanceHostsAsRunHosts(inst.Hosts))
	if role.FlinkJm2 != "192.168.3.41" {
		t.Errorf("引擎 FlinkJm2 = %q，期望 192.168.3.41", role.FlinkJm2)
	}
	if role.HMaster2 != "192.168.3.41" {
		t.Errorf("引擎 HMaster2 = %q，期望 192.168.3.41", role.HMaster2)
	}

	m, ok := bigdataRoleMasters(inst)
	if !ok {
		t.Fatal("bigdataRoleMasters 应识别为 HA 实例")
	}
	if m["flink_jm2"] != role.FlinkJm2 || m["hbase_hm2"] != role.HMaster2 {
		t.Errorf("探活矩阵与引擎不一致：masters=%v role=%+v", m, role)
	}
	if !strings.Contains(m["hdfs_jns"], role.Nn1) || !strings.Contains(m["hdfs_jns"], role.Nn2) {
		t.Errorf("JNS 应包含 NN1/NN2：%q", m["hdfs_jns"])
	}
}

// 41 的访问入口不应再被标成 YARN NM / Spark Worker / HBase RS。
func TestBigdataVerifyEndpoints_HASecondaryHost(t *testing.T) {
	inst, instParams := bigdataHAFixture()
	merged := mergeParamMaps(instParams, parseJSONMap(inst.Hosts[1].ParamsJSON))
	overrideBigdataMasters(inst, merged)
	comps := parseComponentsCSV(merged["components"])
	masters := parseJSONMap(merged["masters"])

	eps := bigdataVerifyEndpoints(inst.Hosts[1], comps, masters, merged)
	names := make([]string, 0, len(eps))
	for _, e := range eps {
		names = append(names, e.Name)
	}
	t.Logf("41 入口 = %v", names)
	for _, want := range []string{"HDFS NameNode-2", "YARN RM-2", "Spark Master-2", "Flink JM-2", "HBase Backup Master", "Hive Metastore-2", "HiveServer2-2"} {
		if !containsString(names, want) {
			t.Errorf("41 入口应包含 %s，实际 %v", want, names)
		}
	}
	for _, bad := range []string{"YARN NM", "Spark Worker", "HBase RS"} {
		if containsString(names, bad) {
			t.Errorf("41 入口不应包含 %s，实际 %v", bad, names)
		}
	}
}
