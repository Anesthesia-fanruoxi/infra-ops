package bigdata

import (
	"testing"
)

// validate_test.go：大数据底座组件校验用例（原 api/stack 的 TestValidateBigdataRemove 与
// TestValidateBigdataAdd_DepSatisfiedByInstalled 整体迁入，断言逐条保留）。

func TestValidateRemove(t *testing.T) {
	installed := []string{"hdfs", "spark", "hive", "trino"}
	if err := validateRemove(nil, installed, false); err == nil {
		t.Fatal("expected empty removing")
	}
	if err := validateRemove([]string{"hdfs"}, installed, false); err == nil {
		t.Fatal("expected refuse hdfs")
	}
	if err := validateRemove([]string{"yarn"}, installed, false); err == nil {
		t.Fatal("expected not installed")
	}
	if err := validateRemove([]string{"hive"}, installed, false); err == nil {
		t.Fatal("expected trino depends hive")
	}
	if err := validateRemove([]string{"trino"}, installed, false); err != nil {
		t.Fatal(err)
	}
	if err := validateRemove([]string{"hive", "trino"}, installed, false); err != nil {
		t.Fatal(err)
	}
	// HA 模式：trino 独立卸载成功；但 zookeeper 是 yarn/spark/flink/hbase/hive 基座，有残留依赖时拒绝
	if err := validateRemove([]string{"trino"}, installed, true); err != nil {
		t.Fatal("HA 卸载 trino 应成功: " + err.Error())
	}
	haInstalled := []string{"hdfs", "zookeeper", "yarn", "spark", "flink", "hive", "metastore_db"}
	if err := validateRemove([]string{"zookeeper"}, haInstalled, true); err == nil {
		t.Fatal("HA 模式残留 yarn/spark/flink/hive 时卸载 zookeeper 应被拒绝")
	}
	if err := validateRemove([]string{"metastore_db"}, haInstalled, true); err == nil {
		t.Fatal("HA 模式残留 hive 时卸载 metastore_db 应被拒绝")
	}
	// 卸载全部 ZK 依赖组件后才允许卸载 zookeeper
	if err := validateRemove([]string{"zookeeper", "yarn", "spark", "flink", "hive", "metastore_db"}, haInstalled, true); err != nil {
		t.Fatalf("卸载全部 ZK 依赖组件后卸载 zookeeper 应成功: %v", err)
	}
}

func TestValidateAdd_DepSatisfiedByInstalled(t *testing.T) {
	installed := []string{"hdfs", "zookeeper", "yarn", "spark", "flink", "hbase", "metastore_db", "hive"}
	if err := validateAdd([]string{"trino"}, installed); err != nil {
		t.Fatalf("已安装 hive 时加装 trino 应通过，got: %v", err)
	}
	// 反向：集群无 hive 时加装 trino 仍须拒绝
	if err := validateAdd([]string{"trino"}, []string{"hdfs", "zookeeper"}); err == nil {
		t.Fatal("缺少 hive 时加装 trino 应被拒绝")
	}
	// 同时加装 hive + trino 也应通过
	if err := validateAdd([]string{"hive", "trino"}, []string{"hdfs", "zookeeper"}); err != nil {
		t.Fatalf("同时加装 hive+trino 应通过，got: %v", err)
	}
	// HBase 依赖 ZooKeeper 同理
	noHbase := []string{"hdfs", "zookeeper", "yarn", "hive"}
	if err := validateAdd([]string{"hbase"}, noHbase); err != nil {
		t.Fatalf("已安装 zookeeper 时加装 hbase 应通过，got: %v", err)
	}
	if err := validateAdd([]string{"hbase"}, []string{"hdfs", "hive"}); err == nil {
		t.Fatal("缺少 zookeeper 时加装 hbase 应被拒绝")
	}
	// 基础约束不回退：重复加装 / 加装 hdfs / 非法组件
	if err := validateAdd([]string{"hive"}, installed); err == nil {
		t.Fatal("重复加装已安装组件应被拒绝")
	}
	if err := validateAdd([]string{"hdfs"}, installed); err == nil {
		t.Fatal("加装 hdfs 应被拒绝")
	}
	if err := validateAdd([]string{"nope"}, installed); err == nil {
		t.Fatal("非法组件应被拒绝")
	}
}
