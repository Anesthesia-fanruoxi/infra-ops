// Package bigdata 大数据底座套件的执行契约：蓝图、阶段脚本、角色规划与 HA 矩阵。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包
// （否则与 store/registry.go 形成循环）；套件之间也不互相 import。
package bigdata

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver 大数据底座驱动（组合部署单模式，HA 由 ha 参数开启）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "bigdata" }

// SelfPlans bigdata 的角色计划是清单式（自建主机行 + Injected/Masters 投影），
// 扩缩容需「落点冻结」语义。引擎据此分流，不再判 key。
func (d *Driver) SelfPlans() bool { return true }

// Phase 阶段脚本：cluster 模式 node.sh（含 7 份注入配置）+ bootstrap.sh。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if mode != "cluster" {
		return stackkit.PhaseFile{}, false
	}
	switch phase {
	case stackkit.PhaseNode:
		return stackkit.PhaseFile{
			Path: "stacks/bigdata/scripts/node.sh",
			Assets: map[string]string{
				"CORE_SITE":   "stacks/bigdata/configs/hdfs/core-site.xml",
				"HDFS_SITE":   "stacks/bigdata/configs/hdfs/hdfs-site.xml",
				"MAPRED_SITE": "stacks/bigdata/configs/hdfs/mapred-site.xml",
				"YARN_SITE":   "stacks/bigdata/configs/yarn/yarn-site.xml",
				"HIVE_SITE":   "stacks/bigdata/configs/hive/hive-site.xml",
				"HBASE_SITE":  "stacks/bigdata/configs/hbase/hbase-site.xml",
				"HA_SH":       "stacks/bigdata/scripts/ha.sh",
			},
		}, true
	case stackkit.PhaseBootstrap:
		return stackkit.PhaseFile{Path: "stacks/bigdata/scripts/bootstrap.sh"}, true
	}
	return stackkit.PhaseFile{}, false
}

// Pipeline 主脑编排的多阶段流水线：清理 → ZK → 格式化并启动 NameNode → 再启动 DataNode（DN 等 NN）→
// YARN → Spark → Flink → Hive → HBase → Trino → 校验。每阶段全机成功才进入下一步，
// 修复旧版「先起 DN 后格式化 NN」的顺序问题，并在第 1 阶段自动清理上次失败残留。
// Component/FullOnly/Always 供「加装组件 / 扩容」裁剪阶段：加装只跑新增组件对应的阶段
// （reset 标 FullOnly 故绝不在加装时执行，避免清掉既有数据）。
func (d *Driver) Pipeline(mode string) []model.StackPhase {
	if mode != "cluster" {
		return nil
	}
	return []model.StackPhase{
		{Key: "reset", Label: "清理旧环境与残留容器", Target: "all", FullOnly: true},
		{Key: "zookeeper", Label: "启动 ZooKeeper 集群", Target: "all", Component: "zookeeper"},
		{Key: "hdfs_boot", Label: "初始化并启动 NameNode/JournalNode", Target: "all", Component: "hdfs"},
		{Key: "hdfs_dn", Label: "启动 DataNode", Target: "all", Component: "hdfs"},
		{Key: "yarn", Label: "启动 YARN", Target: "all", Component: "yarn"},
		{Key: "spark", Label: "启动 Spark", Target: "all", Component: "spark"},
		{Key: "flink", Label: "启动 Flink", Target: "all", Component: "flink"},
		{Key: "hive", Label: "启动 Hive", Target: "all", Component: "hive,metastore_db"},
		{Key: "hbase", Label: "启动 HBase", Target: "all", Component: "hbase"},
		{Key: "trino", Label: "启动 Trino", Target: "all", Component: "trino"},
		{Key: "verify", Label: "校验集群状态并初始化目录", Target: "leader", Always: true},
	}
}

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入，字段与顺序零变更）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
		Key:            "bigdata",
		Name:           "大数据底座",
		Description:    "组合套件：一组机器按主/从角色一次装齐。HDFS 存储（3.3.6）必选打底，ZooKeeper（3.9）/ YARN / Spark（3.5.1）/ Flink（1.19.1）/ Hive（4.0.0）/ HBase（2.5.10）/ Trino（435）按需勾选。镜像均内置 JRE/JDK（基线 Java 11），主机无需安装 JDK。",
		RequiresDocker: true,
		Category:       "platform",
		HaSupport:      true,
		Modes: []model.StackMode{
			{
				Key: "cluster", Label: "组合部署",
				Description: "HDFS 必选（1 NameNode + N DataNode）；可选 ZooKeeper、YARN、Spark、Flink、Hive、HBase、Trino，按组件指定主角色主机，一次部署成型。",
				MinHosts:    2, HostHint: "至少 2 台；勾选主机后按组件指定 NameNode / Master / JobManager 等主角色",
				AssignMaster: true, HasBootstrap: true, DefaultHomeDir: "/data/bigdata",
			},
		},
		SharedVars: []model.StackVar{
			{Name: "components", Label: "部署组件", Required: true},
			{Name: "ha", Label: "高可用", Default: "false", Type: "bool", Required: false},
			// 下列为 HA 专属变量：仅 ha=true 的对应组件需要；均带默认值（ha.sh 对空密码兜底 HiveDb@123）故非必填，避免非 HA 组合被强制校验
			{Name: "hdfs_nameservice", Label: "HDFS HA · Nameservice ID", Default: "ns1"},
			{Name: "jn_http_port", Label: "JournalNode HTTP 端口", Default: "8484"},
			{Name: "jn_rpc_port", Label: "JournalNode RPC 端口", Default: "8485"},
			{Name: "hive_db_image", Label: "Hive HA · 元数据库镜像", Default: "mysql:8.4"},
			{Name: "hive_db_password", Label: "Hive HA · 元数据库密码", Default: ""},
			// 自建镜像仓库（选填）：前端渲染「自建仓库」下拉，与部署中心 hub 镜像源同源；
			// 选中后平台统一预热并改写全部镜像参数（引擎 privatizeImages 消费本键）
			{Name: "image_registry", Label: "镜像仓库（自建，选填）", Type: "registry", Default: ""},
			{Name: "image", Label: "Hadoop 镜像", Default: "apache/hadoop:3.3.6", Required: true},
			{Name: "nn_rpc_port", Label: "NameNode RPC 端口", Default: "9000", Required: true},
			{Name: "replication", Label: "HDFS 副本数", Default: "2", Required: true},
			{Name: "image_zookeeper", Label: "ZooKeeper 镜像", Default: "zookeeper:3.9", Required: true},
			{Name: "nm_mem", Label: "YARN · 每 NodeManager 可用内存(MB)", Default: "8192", Required: true},
			{Name: "nm_vcores", Label: "YARN · 每 NodeManager 可用核数", Default: "4", Required: true},
			{Name: "image_spark", Label: "Spark 镜像", Default: "apache/spark:3.5.1", Required: true},
			{Name: "master_port", Label: "Spark · Master RPC 端口", Default: "7077", Required: true},
			{Name: "webui_port", Label: "Spark · Master WebUI 端口", Default: "8080", Required: true},
			{Name: "worker_cores", Label: "Spark · 每 Worker 核数", Default: "4", Required: true},
			{Name: "worker_mem", Label: "Spark · 每 Worker 内存", Default: "8g", Required: true},
			{Name: "image_flink", Label: "Flink 镜像", Default: "flink:1.19.1", Required: true},
			{Name: "jm_rpc_port", Label: "Flink · JobManager RPC 端口", Default: "6123", Required: true},
			{Name: "tm_slots", Label: "Flink · 每 TaskManager Slots", Default: "4", Required: true},
			{Name: "image_hive", Label: "Hive 镜像", Default: "apache/hive:4.0.0", Required: true},
			{Name: "image_hbase", Label: "HBase 镜像", Default: "openeuler/hbase:latest", Required: true},
			{Name: "image_trino", Label: "Trino 镜像", Default: "trinodb/trino:435", Required: true},
			{Name: "trino_http_port", Label: "Trino · HTTP 端口", Default: "8083", Required: true},
			{Name: "trino_mem", Label: "Trino · JVM 最大堆", Default: "4G", Required: true},
		},
		HostVars: []model.StackVar{
			{Name: "home_dir", Label: "服务主目录", Default: "/data/bigdata", Required: true},
		},
	}
}
