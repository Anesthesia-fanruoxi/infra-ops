package store

import (
	"embed"
	"fmt"
	"strings"

	"infra-ops/model"
)

//go:embed stacks
var stackFS embed.FS

type stackPhaseFile struct {
	path   string
	assets map[string]string // @@KEY@@ -> stacks/... 相对路径
}

// BuiltinStack 内置套件（蓝图 + 脚本路径）。脚本与配置在 store/stacks/<套件>/{scripts,configs}/。
type BuiltinStack struct {
	model.StackBlueprint
	nodeByMode       map[string]stackPhaseFile
	bootstrapByMode  map[string]stackPhaseFile
	scaleOutByMode   map[string]stackPhaseFile
	scaleInByMode    map[string]stackPhaseFile
	// pipelineByMode：ordered 多阶段流水线（服务端主脑编排，替代 node→bootstrap 单步）。
	// 定义后，execute 将按顺序逐阶段调度主机，阶段间全机成功才进入下一阶段。
	pipelineByMode map[string][]model.StackPhase
}

var builtinStacks = []BuiltinStack{
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "redis",
			Name:           "Redis",
			Description:    "一次成型部署 Redis 主从、哨兵或集群。单机请走「基础建设」中的部署 Redis。",
			RequiresDocker: true,
			Category:       "service",
			Modes: []model.StackMode{
				{
					Key: "replication", Label: "主从",
					Description: "1 主 N 从，指定一台为主节点，其余自动为从。",
					MinHosts:    2, HostHint: "至少 2 台，并指定一台主节点",
					AssignMaster: true, DefaultHomeDir: "/data/redis-repl",
				},
				{
					Key: "sentinel", Label: "哨兵",
					Description: "1 主 N 从，每台同时跑 Redis 与 Sentinel，自动故障切换。",
					MinHosts:    3, HostHint: "至少 3 台（建议奇数），并指定一台主节点",
					AssignMaster: true, DefaultHomeDir: "/data/redis-sentinel",
				},
				{
					Key: "cluster", Label: "集群",
					Description: "三主或三主三从，全员节点起来后自动 CLUSTER CREATE。",
					MinHosts:    3, HostHint: "三主至少 3 台；三主三从至少 6 台且为偶数",
					HasBootstrap: true, DefaultHomeDir: "/data/redis-cluster",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "password", Label: "访问密码", Required: true},
				{Name: "image", Label: "镜像", Default: "redis:7", Required: true},
				{Name: "replicas", Label: "从节点倍数", Default: "0", Required: true, Modes: []string{"cluster"}},
				{Name: "sentinel_port", Label: "哨兵端口", Default: "26379", Required: true, Modes: []string{"sentinel"}},
			},
			HostVars: []model.StackVar{
				{Name: "port", Label: "端口", Default: "6379", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/redis", Required: true},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"replication": {
				path: "stacks/redis/scripts/repl-node.sh",
				assets: map[string]string{
					"REDIS_MASTER_CONF":  "stacks/redis/configs/repl-master.conf",
					"REDIS_REPLICA_CONF": "stacks/redis/configs/repl-replica.conf",
				},
			},
			"cluster": {
				path: "stacks/redis/scripts/cluster-node.sh",
				assets: map[string]string{
					"REDIS_CLUSTER_CONF": "stacks/redis/configs/cluster.conf",
				},
			},
			"sentinel": {
				path: "stacks/redis/scripts/sentinel-node.sh",
				assets: map[string]string{
					"REDIS_SENTINEL_MASTER_CONF":  "stacks/redis/configs/sentinel-master.conf",
					"REDIS_SENTINEL_REPLICA_CONF": "stacks/redis/configs/sentinel-replica.conf",
					"REDIS_SENTINEL_CONF":         "stacks/redis/configs/sentinel.conf",
				},
			},
		},
		bootstrapByMode: map[string]stackPhaseFile{
			"cluster": {path: "stacks/redis/scripts/cluster-bootstrap.sh"},
		},
		scaleOutByMode: map[string]stackPhaseFile{
			"cluster": {path: "stacks/redis/scripts/cluster-add.sh"},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
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
				// 选填：填入内网/私有镜像仓库前缀（如 192.168.7.13:5000）后，下方所有镜像参数会自动改写为 <前缀>/<原镜像>，无需逐个修改
				{Name: "image_registry", Label: "私有镜像仓库前缀（选填，填后自动应用到下列全部镜像）", Default: ""},
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
		},
		nodeByMode: map[string]stackPhaseFile{
			"cluster": {
				path: "stacks/bigdata/scripts/node.sh",
				assets: map[string]string{
					"CORE_SITE":   "stacks/bigdata/configs/hdfs/core-site.xml",
					"HDFS_SITE":   "stacks/bigdata/configs/hdfs/hdfs-site.xml",
					"MAPRED_SITE": "stacks/bigdata/configs/hdfs/mapred-site.xml",
					"YARN_SITE":   "stacks/bigdata/configs/yarn/yarn-site.xml",
					"HIVE_SITE":   "stacks/bigdata/configs/hive/hive-site.xml",
					"HBASE_SITE":  "stacks/bigdata/configs/hbase/hbase-site.xml",
					"HA_SH":       "stacks/bigdata/scripts/ha.sh",
				},
			},
		},
		bootstrapByMode: map[string]stackPhaseFile{
			"cluster": {path: "stacks/bigdata/scripts/bootstrap.sh"},
		},
		// 主脑编排的多阶段流水线：清理 → ZK → 格式化并启动 NameNode → 再启动 DataNode（DN 等 NN）→
		// YARN → Spark → Flink → Hive → HBase → Trino → 校验。每阶段全机成功才进入下一步，
		// 修复旧版"先起 DN 后格式化 NN"的顺序问题，并在第 1 阶段自动清理上次失败残留。
		// Component/FullOnly/Always 供「加装组件 / 扩容」裁剪阶段：加装只跑新增组件对应的阶段
		// （reset 标 FullOnly 故绝不在加装时执行，避免清掉既有数据）。
		pipelineByMode: map[string][]model.StackPhase{
			"cluster": {
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
			},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "kafka",
			Name:           "Kafka",
			Category:       "service",
			Description:    "一次成型部署 Kafka 集群：KRaft 内置仲裁（免 ZooKeeper）或传统 ZooKeeper 模式。节点间经宿主机 IP 互通，advertised.listeners 自动指向本机；局域网明文监听，未启用 SASL。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "kraft", Label: "KRaft 集群",
					Description: "Kafka 3.x 内置仲裁，broker+controller 混部，每台一容器，免 ZooKeeper；首次启动自动 format 存储。",
					MinHosts:    1, HostHint: "生产建议 ≥3 台且为奇数（controller 仲裁）",
					DefaultHomeDir: "/data/kafka-kraft",
				},
				{
					Key: "zk", Label: "ZooKeeper 集群",
					Description: "每台同时部署一个 ZooKeeper 节点组成 ensemble，外加 Kafka broker，共 2 容器。",
					MinHosts:    1, HostHint: "生产建议 3 或 5 台奇数（ZK 仲裁）",
					DefaultHomeDir: "/data/kafka-zk",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "image", Label: "Kafka 镜像", Default: "apache/kafka:3.7.0", Required: true},
				{Name: "mem", Label: "JVM 堆内存", Default: "1g", Required: true},
				{Name: "controller_port", Label: "Controller 端口", Default: "9093", Required: true, Modes: []string{"kraft"}},
				{Name: "cluster_id", Label: "集群 ID（留空自动生成）", Default: "", Modes: []string{"kraft"}},
				{Name: "zk_image", Label: "ZooKeeper 镜像", Default: "zookeeper:3.9", Required: true, Modes: []string{"zk"}},
				{Name: "zk_port", Label: "ZK 客户端端口", Default: "2181", Required: true, Modes: []string{"zk"}},
				{Name: "enable_ui", Label: "附带部署 KafkaUI（yes/no）", Default: "no", Required: true},
				{Name: "ui_image", Label: "KafkaUI 镜像", Default: "provectuslabs/kafka-ui:v0.7.2", Required: true},
				{Name: "ui_port", Label: "KafkaUI 端口", Default: "8080", Required: true},
			},
			HostVars: []model.StackVar{
				{Name: "port", Label: "Broker 端口", Default: "9092", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/kafka-kraft", Required: true, Modes: []string{"kraft"}},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/kafka-zk", Required: true, Modes: []string{"zk"}},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"kraft": {path: "stacks/kafka/scripts/kraft-node.sh"},
			"zk":    {path: "stacks/kafka/scripts/zk-node.sh"},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "elasticsearch",
			Name:           "Elasticsearch",
			Category:       "service",
			Description:    "一次成型部署 Elasticsearch 集群。双模式：原「集群」模式（指定一台引导主节点，其余 seed_hosts 加入）；新增「冷热温」模式（按主机勾选 master/纯协调/数据层角色，主脑流水线阶段编排，支持 SSL 自签）。host 网络直连，默认关闭 xpack 安全。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "集群",
					Description: "1 引导 master + N data，节点名/种子地址由平台注入，无需手工填 is_bootstrap。",
					MinHosts:    1, HostHint: "至少 1 台；多节点请指定一台引导主节点，生产建议 ≥3 台",
					AssignMaster: true, DefaultHomeDir: "/data/elasticsearch",
				},
				{
					Key: "cold_warm_hot", Label: "冷热温",
					Description: "按数据热度分层：每台多选 master/纯协调/数据-hot/warm/cold 角色；master 不与数据层混部；生产建议 master ≥3 台（奇数）。SSL 可自签。",
					MinHosts:    4, HostHint: "至少 4 台；建议 1+ master（≥3，奇数）+ ≥2 纯协调 + 数据层（按 tier）",
					HasBootstrap: true, DefaultHomeDir: "/data/elasticsearch",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "cluster_name", Label: "集群名", Default: "es-cluster", Required: true},
				{Name: "image", Label: "镜像", Default: "elasticsearch:9.5.3", Required: true},
				{Name: "java_opts", Label: "堆内存", Default: "-Xms1g -Xmx1g", Required: true, Modes: []string{"cluster"}},
				{Name: "transport_port", Label: "Transport 端口", Default: "9300", Required: true},
				// 冷热温模式级变量：SSL 开关、纯协调数量、JVM 堆与额外参数
				{Name: "ssl_enabled", Label: "启用 SSL（节点自签证书）", Default: "false", Type: "bool", Required: false, Modes: []string{"cold_warm_hot"}},
				{Name: "coordinator_count", Label: "纯协调节点数", Default: "2", Required: false, Modes: []string{"cold_warm_hot"}},
				{Name: "heap_xms", Label: "JVM 初始堆", Default: "1g", Required: false, Modes: []string{"cold_warm_hot"}},
				{Name: "heap_xmx", Label: "JVM 最大堆", Default: "1g", Required: false, Modes: []string{"cold_warm_hot"}},
				{Name: "jvm_opts", Label: "JVM 额外参数（追加进 ES_JAVA_OPTS）", Default: "-XX:+UseG1GC", Required: false, Modes: []string{"cold_warm_hot"}},
			},
			HostVars: []model.StackVar{
				{Name: "port", Label: "HTTP 端口", Default: "9200", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/elasticsearch", Required: true},
				// 冷热温：每台主机的角色勾选（逗号分隔 master/coordinator/data_hot,data_warm,data_cold），
				// 声明为 HostVar 以便 mergeStackParams 持久化到主机 ParamsJSON，后端与脚本据此派生 node.roles。
				{Name: "roles", Label: "节点角色", Default: "coordinator", Required: false, Modes: []string{"cold_warm_hot"}},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"cluster": {
				path: "stacks/elasticsearch/scripts/node.sh",
				assets: map[string]string{
					"ES_MASTER_YML": "stacks/elasticsearch/configs/elasticsearch-master.yml",
					"ES_MEMBER_YML": "stacks/elasticsearch/configs/elasticsearch-member.yml",
				},
			},
			"cold_warm_hot": {
				path: "stacks/elasticsearch/scripts/cold-warm-hot/node.sh",
				// 角色动态（node.roles/初识 master/SSL 条件块），elasticsearch.yml 由脚本内联生成，不走静态素材
			},
		},
		bootstrapByMode: map[string]stackPhaseFile{
			"cold_warm_hot": {path: "stacks/elasticsearch/scripts/cold-warm-hot/bootstrap.sh"},
		},
		scaleInByMode: map[string]stackPhaseFile{
			"cold_warm_hot": {path: "stacks/elasticsearch/scripts/cold-warm-hot/scale_in.sh"},
		},
		// 冷热温模式主脑流水线：reset（清残留）→ masters → coords → data → verify（leader 校验）。
		// reset 标 FullOnly 故扩容/加装时绝不清空既有数据。
		pipelineByMode: map[string][]model.StackPhase{
			"cold_warm_hot": {
				{Key: "reset", Label: "清理旧环境与残留容器", Target: "all", FullOnly: true},
				{Key: "masters", Label: "启动 master 候选节点", Target: "all"},
				{Key: "coords", Label: "启动纯协调节点", Target: "all"},
				{Key: "data", Label: "启动数据节点（hot/warm/cold）", Target: "all"},
				{Key: "verify", Label: "校验集群健康与各分层", Target: "leader", Always: true},
			},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "rabbitmq",
			Name:           "RabbitMQ",
			Category:       "service",
			Description:    "一次成型部署 RabbitMQ 集群。指定一台初始化节点，其余自动 join；host 网络 + 节点名 rabbit@宿主机IP，Erlang cookie 留空则自动生成。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "集群",
					Description: "1 初始化节点 + N 加入节点，管理插件默认开启。",
					MinHosts:    1, HostHint: "至少 1 台；多节点请指定一台初始化节点，生产建议 ≥3 台奇数",
					AssignMaster: true, DefaultHomeDir: "/data/rabbitmq",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "image", Label: "镜像", Default: "rabbitmq:3.13-management", Required: true},
				{Name: "admin_user", Label: "管理员账号", Default: "admin", Required: true},
				{Name: "admin_pass", Label: "管理员密码", Required: true},
				{Name: "erlang_cookie", Label: "Erlang Cookie（留空自动生成）"},
				{Name: "amqp_port", Label: "AMQP 端口", Default: "5672", Required: true},
				{Name: "mgmt_port", Label: "管理端口", Default: "15672", Required: true},
			},
			HostVars: []model.StackVar{
				{Name: "home_dir", Label: "服务主目录", Default: "/data/rabbitmq", Required: true},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"cluster": {
				path: "stacks/rabbitmq/scripts/node.sh",
				assets: map[string]string{
					"RABBITMQ_CONF": "stacks/rabbitmq/configs/rabbitmq.conf",
				},
			},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "rocketmq",
			Name:           "RocketMQ",
			Category:       "service",
			Description:    "一步成型部署 RocketMQ 集群：指定一台 Master，首节点自动部署 NameServer + Broker Master，其余节点自动成为同组 Broker Slave 加入（无需先分两步布 NameServer 与 Broker）。全容器 host 网络，brokerIP1 自动取本机 IP。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "一步成型",
					Description: "首节点（Master）部署 NameServer + Broker Master（brokerId=0），其余节点自动作为同组 Slave（brokerId>0）加入；namesrvAddr 自动指向首节点。",
					MinHosts:    1, HostHint: "至少 1 台；多节点请指定一台 Master，其余自动为同组 Slave（生产建议 ≥2 台）",
					AssignMaster: true, DefaultHomeDir: "/data/rocketmq",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "image", Label: "镜像", Default: "apache/rocketmq:4.9.7", Required: true},
				{Name: "cluster_name", Label: "集群名", Default: "rmq-cluster", Required: true},
				{Name: "broker_name", Label: "Broker 组名（主备同组同名）", Default: "broker-a", Required: true},
				{Name: "broker_port", Label: "Broker 端口", Default: "10911", Required: true},
			},
			HostVars: []model.StackVar{
				{Name: "ns_port", Label: "NameServer 端口", Default: "9876", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/rocketmq", Required: true},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"cluster": {
				path: "stacks/rocketmq/scripts/node.sh",
				assets: map[string]string{
					"BROKER_MASTER_CONF": "stacks/rocketmq/configs/broker-master.conf",
					"BROKER_SLAVE_CONF":  "stacks/rocketmq/configs/broker-slave.conf",
				},
			},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "nacos",
			Name:           "Nacos",
			Category:       "service",
			Description:    "一次成型部署 Nacos 集群。指定一台引导节点，首节点自动部署 MySQL 并初始化 nacos 库，其余节点经 NACOS_SERVERS（全节点 ip:port）组集群；host 网络，控制台/gRPC 端口直通宿主机。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "集群",
					Description: "1 引导节点（含 MySQL）+ N 成员，全节点写同一 MySQL，NACOS_SERVERS 自动枚举全部节点，NACOS_AUTH_TOKEN 全节点一致。",
					MinHosts:    1, HostHint: "至少 1 台；多节点请指定一台引导节点（首台自动部署共享 MySQL）",
					AssignMaster: true, DefaultHomeDir: "/data/nacos",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "image", Label: "Nacos 镜像", Default: "nacos/nacos-server:v2.3.2", Required: true},
				{Name: "cluster_name", Label: "集群名", Default: "nacos-cluster", Required: true},
				{Name: "nacos_token", Label: "Nacos Auth Token（≥32 字符，全节点一致）", Default: "nacos-cluster-auth-token-0123456789abcdef", Required: true},
				{Name: "db_username", Label: "数据库用户名", Default: "nacos", Required: true},
				{Name: "db_password", Label: "数据库密码", Required: true},
				{Name: "db_host", Label: "外部 MySQL 地址（host[:port]，留空自动在首节点部署）", Default: ""},
				{Name: "mysql_image", Label: "MySQL 镜像（自动部署时用）", Default: "mysql:8.0", Required: true},
			},
			HostVars: []model.StackVar{
				{Name: "port", Label: "Nacos HTTP 端口", Default: "8848", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/nacos", Required: true},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"cluster": {
				path: "stacks/nacos/scripts/node.sh",
				assets: map[string]string{
					"NACOS_SQL": "stacks/nacos/configs/nacos-init.sql",
				},
			},
		},
	},
	{
		StackBlueprint: model.StackBlueprint{
			Key:            "powerjob",
			Name:           "PowerJob",
			Category:       "service",
			Description:    "一次成型部署 PowerJob 调度集群。指定一台引导节点，首节点自动部署 MySQL 并初始化 powerjob 库，其余节点各起一个 powerjob-server（对等集群，worker 连任一节点即可）；host 网络，控制台 7700 直通宿主机。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "集群",
					Description: "1 引导节点（含 MySQL）+ N server，全节点共享同一 MySQL（首站自动初始化表结构），worker 配置任一节点地址即可连接。",
					MinHosts:    1, HostHint: "至少 1 台；多节点请指定一台引导节点（首台自动部署共享 MySQL）",
					AssignMaster: true, DefaultHomeDir: "/data/powerjob",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "image", Label: "PowerJob server 镜像", Default: "powerjob/powerjob-server:latest", Required: true},
				{Name: "db_username", Label: "数据库用户名", Default: "powerjob", Required: true},
				{Name: "db_password", Label: "数据库密码", Required: true},
				{Name: "db_host", Label: "外部 MySQL 地址（host[:port]，留空自动在首节点部署）", Default: ""},
				{Name: "mysql_image", Label: "MySQL 镜像（自动部署时用）", Default: "mysql:8.0", Required: true},
				{Name: "akka_port", Label: "Akka 通信端口", Default: "10086", Required: true},
			},
			HostVars: []model.StackVar{
				{Name: "server_port", Label: "控制台端口", Default: "7700", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/powerjob", Required: true},
			},
		},
		nodeByMode: map[string]stackPhaseFile{
			"cluster": {path: "stacks/powerjob/scripts/node.sh"},
		},
	},
}

// ListStackBlueprints 返回内置套件蓝图（不含脚本正文）。
func ListStackBlueprints() []model.StackBlueprint {
	out := make([]model.StackBlueprint, 0, len(builtinStacks))
	for _, s := range builtinStacks {
		out = append(out, s.StackBlueprint)
	}
	return out
}

// FindBuiltinStack 按 key 取内置套件。
func FindBuiltinStack(key string) *BuiltinStack {
	for i := range builtinStacks {
		if builtinStacks[i].Key == key {
			return &builtinStacks[i]
		}
	}
	return nil
}

// DockerTemplateName 套件 Docker 前置所调用的内置模板名。
const DockerTemplateName = "安装 Docker"

func (s *BuiltinStack) ModeDef(mode string) *model.StackMode {
	for i := range s.Modes {
		if s.Modes[i].Key == mode {
			return &s.Modes[i]
		}
	}
	return nil
}

func (s *BuiltinStack) Blueprint() model.StackBlueprint { return s.StackBlueprint }

// Pipeline 返回套件指定模式的有序流水线阶段；空表示走传统 node→bootstrap 单步流程。
func (s *BuiltinStack) Pipeline(mode string) []model.StackPhase {
	return s.pipelineByMode[mode]
}

func (s *BuiltinStack) LoadPhase(mode, phase string) (string, error) {
	var f stackPhaseFile
	switch phase {
	case "node":
		f = s.nodeByMode[mode]
	case "bootstrap":
		f = s.bootstrapByMode[mode]
	case "scale_out":
		f = s.scaleOutByMode[mode]
	case "scale_in":
		f = s.scaleInByMode[mode]
	default:
		return "", fmt.Errorf("未知阶段: %s", phase)
	}
	if f.path == "" {
		return "", fmt.Errorf("套件 %s 模式 %s 没有 %s 脚本", s.Key, mode, phase)
	}
	b, err := stackFS.ReadFile(f.path)
	if err != nil {
		return "", err
	}
	script := string(b)
	for key, asset := range f.assets {
		c, err := stackFS.ReadFile(asset)
		if err != nil {
			return "", err
		}
		script = strings.ReplaceAll(script, "@@"+key+"@@", string(c))
	}
	return script, nil
}
