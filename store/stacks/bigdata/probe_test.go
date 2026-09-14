package bigdata

import (
	"strings"
	"testing"

	"infra-ops/store/stackkit"
)

// 期望容器与探活解析单测（原 api/stack/stack_verify_test.go 的大数据用例随实现迁入）。

// 按主机 IP + 角色 + masters 矩阵推断本机期望容器（含 HA 落点）。
func containersAt(hostIP, role string, params map[string]string) []string {
	return New().Containers(stackkit.ProbeCtx{HostIP: hostIP, Role: role, Params: params})
}

func TestExpectedContainers(t *testing.T) {
	params := map[string]string{"components": "hdfs,spark,hive", "masters": `{"hdfs":"10.0.0.1","spark":"10.0.0.1","hive":"10.0.0.1"}`}
	master := containersAt("10.0.0.1", "master", params)
	want := []string{"hadoop-namenode", "spark-master", "hive-metastore", "hive-hiveserver2"}
	if strings.Join(master, ",") != strings.Join(want, ",") {
		t.Fatalf("master=%v", master)
	}
	worker := containersAt("10.0.0.2", "worker", params)
	wantW := []string{"hadoop-datanode", "spark-worker"}
	if strings.Join(worker, ",") != strings.Join(wantW, ",") {
		t.Fatalf("worker=%v", worker)
	}
}

func TestExpectedContainersHA(t *testing.T) {
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
	// .1 = NN1/RM1/SparkM1/JM1/HM1/MS1/HS1/HiveDB 主机；
	// JournalNode 在 JNS 全部主机（含 NN1/NN2）部署，DataNode 全节点部署（ha.sh / node.sh 落点）。
	primary := containersAt("10.0.0.1", "master", ha())
	primaryW := []string{"bigdata-zookeeper", "hadoop-namenode", "hadoop-zkfc", "hadoop-journalnode",
		"hadoop-datanode", "hadoop-resourcemanager", "spark-master", "flink-jobmanager",
		"hbase-master", "hive-metastore", "hive-hiveserver2", "trino-node"}
	if strings.Join(primary, ",") != strings.Join(primaryW, ",") {
		t.Fatalf("primary=%v", primary)
	}
	secondary := containersAt("10.0.0.2", "node", ha())
	secW := []string{"bigdata-zookeeper", "hadoop-namenode2", "hadoop-zkfc2", "hadoop-journalnode",
		"hadoop-datanode", "hadoop-resourcemanager2", "spark-master2", "flink-jobmanager2",
		"hbase-backup-master", "hive-metastore2", "hive-hiveserver2-2", "trino-node"}
	if strings.Join(secondary, ",") != strings.Join(secW, ",") {
		t.Fatalf("secondary=%v", secondary)
	}
	// 第三台：JN 之一 + 纯工作节点容器 + metastore_db 主机
	w3 := containersAt("10.0.0.3", "node", ha())
	w3W := []string{"bigdata-zookeeper", "hadoop-journalnode", "hadoop-datanode",
		"hadoop-nodemanager", "spark-worker", "flink-taskmanager", "hbase-regionserver",
		"hive-metastore-db", "trino-node"}
	if strings.Join(w3, ",") != strings.Join(w3W, ",") {
		t.Fatalf("w3=%v", w3)
	}
}

func TestParseSuiteContainers(t *testing.T) {
	raw := `__IO_CTR__hadoop-namenode__=running|1||apache/hadoop:3.3.6|2026-09-09T01:00:00Z
__IO_CTR__spark-master__=running|1||bitnami/spark:3.5|2026-09-09T01:00:00Z
`
	out := New().ParseSuite(stackkit.ProbeCtx{}, raw, []string{"hadoop-namenode", "spark-master"})
	if out.OK == nil || !*out.OK {
		t.Fatalf("want ok, checks=%+v", out.Checks)
	}
	inst := ""
	for _, c := range out.Checks {
		if c.Name == "实例" {
			inst = c.Detail
		}
	}
	if !strings.Contains(inst, "hadoop-namenode") || !strings.Contains(inst, "spark-master") {
		t.Fatalf("inst=%s", inst)
	}

	out2 := New().ParseSuite(stackkit.ProbeCtx{}, `__IO_CTR__hadoop-namenode__=missing`, []string{"hadoop-namenode"})
	if out2.OK == nil || *out2.OK {
		t.Fatalf("missing should fail")
	}
}

// 无期望容器（未勾选组件）时不由套件追加检查项，交给引擎 compose 兜底。
func TestParseSuiteNoContainers(t *testing.T) {
	out := New().ParseSuite(stackkit.ProbeCtx{}, "anything", nil)
	if len(out.Checks) != 0 || out.OK != nil {
		t.Fatalf("空容器应由引擎兜底：%+v", out)
	}
}
