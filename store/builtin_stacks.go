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
	nodeByMode      map[string]stackPhaseFile
	bootstrapByMode map[string]stackPhaseFile
	scaleOutByMode  map[string]stackPhaseFile
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
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "组合部署",
					Description: "HDFS 必选（1 NameNode + N DataNode）；可选 ZooKeeper、YARN、Spark、Flink、Hive、HBase、Trino，统一主节点规划，一次部署成型。",
					MinHosts:    2, HostHint: "至少 2 台，并指定一台主节点（NameNode/Master/JobManager/Hive 同机）",
					AssignMaster: true, HasBootstrap: true, DefaultHomeDir: "/data/bigdata",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "components", Label: "部署组件", Required: true},
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
				{Name: "image_hbase", Label: "HBase 镜像", Default: "apache/hbase:2.5.10", Required: true},
				{Name: "image_trino", Label: "Trino 镜像", Default: "trinodb/trino:435", Required: true},
				{Name: "trino_http_port", Label: "Trino · HTTP 端口", Default: "8080", Required: true},
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
					"CORE_SITE":  "stacks/bigdata/configs/hdfs/core-site.xml",
					"HDFS_SITE":  "stacks/bigdata/configs/hdfs/hdfs-site.xml",
					"YARN_SITE":  "stacks/bigdata/configs/yarn/yarn-site.xml",
					"HIVE_SITE":  "stacks/bigdata/configs/hive/hive-site.xml",
					"HBASE_SITE": "stacks/bigdata/configs/hbase/hbase-site.xml",
				},
			},
		},
		bootstrapByMode: map[string]stackPhaseFile{
			"cluster": {path: "stacks/bigdata/scripts/bootstrap.sh"},
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
			Description:    "一次成型部署 Elasticsearch 集群。指定一台引导主节点（含 initial_master_nodes），其余经 seed_hosts 加入；host 网络直连，默认关闭 xpack 安全。",
			RequiresDocker: true,
			Modes: []model.StackMode{
				{
					Key: "cluster", Label: "集群",
					Description: "1 引导 master + N data，节点名/种子地址由平台注入，无需手工填 is_bootstrap。",
					MinHosts:    1, HostHint: "至少 1 台；多节点请指定一台引导主节点，生产建议 ≥3 台",
					AssignMaster: true, DefaultHomeDir: "/data/elasticsearch",
				},
			},
			SharedVars: []model.StackVar{
				{Name: "cluster_name", Label: "集群名", Default: "es-cluster", Required: true},
				{Name: "image", Label: "镜像", Default: "elasticsearch:8.17.0", Required: true},
				{Name: "java_opts", Label: "堆内存", Default: "-Xms1g -Xmx1g", Required: true},
				{Name: "transport_port", Label: "Transport 端口", Default: "9300", Required: true},
			},
			HostVars: []model.StackVar{
				{Name: "port", Label: "HTTP 端口", Default: "9200", Required: true},
				{Name: "home_dir", Label: "服务主目录", Default: "/data/elasticsearch", Required: true},
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

func (s *BuiltinStack) LoadPhase(mode, phase string) (string, error) {
	var f stackPhaseFile
	switch phase {
	case "node":
		f = s.nodeByMode[mode]
	case "bootstrap":
		f = s.bootstrapByMode[mode]
	case "scale_out":
		f = s.scaleOutByMode[mode]
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
