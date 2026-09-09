// 内置部署模板元数据与加载。
// 脚本统一存于 builtin/*.sh（经 go:embed 嵌入），避免在 Go 字符串字面量中维护 shell，
// 彻底规避转义/缩进污染；非 shell 资源（如 nginx.conf）经占位符 @@KEY@@ 注入脚本。
package store

import (
	"database/sql"
	"embed"
	"errors"
	"strings"
)

//go:embed builtin stacks/elasticsearch stacks/rocketmq stacks/rabbitmq
var builtinFS embed.FS

type builtinTemplate struct {
	name        string
	description string
	category    string            // 功能分类：系统/运行时/容器/镜像仓库/Web·网关/数据库/缓存/消息队列/配置注册中心/搜索引擎/可观测·监控
	variables   string            // JSON: [{name,label,default,required}]
	services    string            // JSON: [{name,url,web}]
	requires    string            // JSON: [{check,hint}] 前置依赖检查
	configs     string            // JSON: [{key,label,file,hint,required}] 可被用户覆盖的配置文件
	path        string            // builtinFS 内相对脚本路径
	assets      map[string]string // 资源路径 -> 注入脚本占位符 @@KEY@@ 的内容
}

// requiresDocker 常见前置依赖：目标主机须已装 Docker。
const requiresDocker = `[{"check":"command -v docker >/dev/null 2>&1","hint":"此模板基于 Docker，请先在主机执行「安装 Docker」模板"}]`

var builtinTemplates = []builtinTemplate{
	{
		name:        "新增 Linux 用户",
		description: "创建带家目录和 bash 的普通用户，已存在则跳过。",
		category:    "系统",
		variables:   `[{"name":"username","label":"用户名","default":"","required":true}]`,
		path:        "builtin/add-linux-user.sh",
	},
	{
		name:        "修改主机名",
		description: "为当前主机设置新主机名：在第三步「自定义变量」中为每台主机分别填写各自的新名字（留空会校验失败）。持久化 hostname、更新 /etc/hosts，并自动同步平台台账中的主机名。",
		category:    "系统",
		variables:   `[{"name":"new_name","label":"新主机名","default":"","required":true}]`,
		path:        "builtin/set-hostname.sh",
	},
	{
		name:        "安装 Docker",
		description: "适配 Rocky/CentOS 9/10：阿里云源安装最新版 Docker CE（含 buildx/compose 插件），写入 daemon.json（镜像加速/日志轮转/数据目录）。已安装则跳过。",
		category:    "工具",
		variables:   `[]`,
		path:        "builtin/install-docker.sh",
	},
	{
		name:        "安装 OpenResty",
		description: "OpenResty 官方源安装最新版（Rocky/EL 8/9；Rocky 10 自动回退 el9 源），并落盘生产级 nginx.conf 主配置（含 cert / conf.d 等目录自动创建、语法校验与 reload）。已安装则跳过安装、仅重写配置。",
		category:    "工具",
		variables:   `[]`,
		services:    `[{"name":"OpenResty","url":"http://{{ip}}","web":true}]`,
		configs:     `[{"key":"nginx_conf","label":"主配置文件 nginx.conf","file":"/usr/local/openresty/nginx/conf/nginx.conf","hint":"默认使用平台生产级 nginx.conf；粘贴完整自定义 nginx.conf 将整体覆盖，写入后仍会做语法校验并 reload","required":false}]`,
		path:        "builtin/install-openresty.sh",
		assets:      map[string]string{"NGINX_CONF": "builtin/openresty-nginx.conf"},
	},
	{
		name:        "安装 OpenJDK",
		description: "二进制包安装指定版本 OpenJDK（华为云镜像源，x64/aarch64 自适应），写入 /etc/profile.d/java.sh 环境变量，同版本已装则跳过。",
		category:    "工具",
		variables:   `[{"name":"version","label":"JDK 大版本","default":"17","required":true},{"name":"install_dir","label":"安装目录","default":"/usr/local","required":true}]`,
		path:        "builtin/install-jdk.sh",
	},
	{
		name:        "部署 MySQL",
		description: "docker compose 部署 MySQL 单机（生成 compose.yml 落盘，改参数后 up -d 平滑生效）：utf8mb4/生产参数 my.cnf（已有则备份）、middleware_net 共享网络、数据目录持久化、等待就绪。root 密码仅首次初始化数据时生效。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"root_password","label":"root 密码","default":"","required":true},{"name":"port","label":"端口","default":"3306","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/mysql","required":true},{"name":"image","label":"镜像","default":"mysql:8.0","required":true}]`,
		path:        "builtin/install-mysql.sh",
		services:    `[{"name":"MySQL","url":"mysql://{{ip}}:{{port}}","web":false}]`,
		configs:     `[{"key":"my_cnf","label":"主配置文件 my.cnf","file":"{{home_dir}}/my.cnf","hint":"默认使用生产参数 my.cnf；粘贴完整自定义 my.cnf（含 [client]/[mysql]/[mysqld] 段）将整体覆盖","required":false}]`,
	},
	{
		name:        "部署 Redis",
		description: "docker compose 部署 Redis（生成 compose.yml 落盘）：密码认证、AOF 持久化、middleware_net 共享网络、数据目录持久化、健康检查。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"password","label":"访问密码","default":"","required":true},{"name":"port","label":"端口","default":"6379","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/redis","required":true},{"name":"image","label":"镜像","default":"redis:7","required":true}]`,
		path:        "builtin/install-redis.sh",
		services:    `[{"name":"Redis","url":"redis://{{ip}}:{{port}}","web":false}]`,
	},
	{
		name:        "部署 MongoDB",
		description: "docker compose 部署 MongoDB（生成 compose.yml 落盘）：自定义 mongod.conf（已有则备份）、middleware_net 共享网络、数据目录持久化，首次自动创建管理员用户（已存在则跳过）。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"admin_username","label":"管理员用户名","default":"admin","required":true},{"name":"admin_password","label":"管理员密码","default":"","required":true},{"name":"port","label":"端口","default":"27017","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/mongodb","required":true},{"name":"image","label":"镜像","default":"mongo:6.0","required":true}]`,
		path:        "builtin/install-mongo.sh",
		services:    `[{"name":"MongoDB","url":"mongodb://{{ip}}:{{port}}","web":false}]`,
	},
	{
		name:        "部署 RabbitMQ",
		description: "docker compose 部署 RabbitMQ（management 版，生成 compose.yml 落盘）：middleware_net 共享网络、可选延迟队列插件（GitHub 官方源下载，多加速地址轮换重试，已存在则跳过）、插件启用状态校验。依赖 Docker。",
		category:    "消息队列",
		requires:    requiresDocker,
		variables:   `[{"name":"home_dir","label":"服务主目录","default":"/data/rabbitmq","required":true},{"name":"username","label":"管理员用户名","default":"root","required":true},{"name":"password","label":"管理员密码","default":"","required":true},{"name":"amqp_port","label":"AMQP 端口","default":"5672","required":true},{"name":"mgmt_port","label":"管理界面端口","default":"15672","required":true},{"name":"delayed_plugin","label":"安装延迟队列插件(yes/no)","default":"yes","required":true},{"name":"image","label":"镜像","default":"rabbitmq:3.11.15-management","required":true}]`,
		path:        "builtin/install-rabbitmq.sh",
		services:    `[{"name":"RabbitMQ 管理","url":"http://{{ip}}:{{mgmt_port}}","web":true}]`,
	},
	{
		name:        "部署 Nacos",
		description: "docker compose 部署 Nacos 单机版（MySQL 存储，生成 compose.yml 落盘）：自动初始化 nacos 数据库与表结构（已初始化则跳过）、middleware_net 网络容器名互连、控制台就绪检查。须先在同一主机执行「部署 MySQL」。",
		category:    "配置注册中心",
		requires:    requiresDocker,
		variables:   `[{"name":"home_dir","label":"服务主目录","default":"/data/nacos","required":true},{"name":"db_username","label":"数据库用户名","default":"nacos","required":true},{"name":"db_password","label":"数据库密码","default":"","required":true},{"name":"port","label":"控制台端口","default":"8848","required":true},{"name":"grpc_port","label":"gRPC 端口","default":"9848","required":true},{"name":"image","label":"镜像","default":"nacos/nacos-server:v2.1.0","required":true}]`,
		path:        "builtin/install-nacos.sh",
		services:    `[{"name":"Nacos 控制台","url":"http://{{ip}}:{{port}}/nacos/","web":true}]`,
		assets:      map[string]string{"NACOS_SQL": "builtin/nacos-init.sql"},
	},
	{
		name:        "部署 Elasticsearch",
		description: "docker compose 部署 Elasticsearch 单机（生成 compose.yml 落盘，开启安全认证、关闭 HTTPS）：数据目录自动授权 uid=1000、Docker 原生健康检查、JVM 内存可调。elastic 密码仅首次初始化数据时生效。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"elastic_password","label":"elastic 密码","default":"","required":true},{"name":"port","label":"端口","default":"9200","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/elasticsearch","required":true},{"name":"java_opts","label":"JVM 内存","default":"-Xms512m -Xmx512m","required":true},{"name":"image","label":"镜像","default":"docker.elastic.co/elasticsearch/elasticsearch:8.11.0","required":true}]`,
		path:        "builtin/install-elasticsearch.sh",
		services:    `[{"name":"Elasticsearch","url":"http://{{ip}}:{{port}}","web":true}]`,
	},
	{
		name:        "部署 Kibana",
		description: "docker compose 部署 Kibana（生成 compose.yml 落盘）：自动通过 ES API 设置 kibana_system 密码（幂等覆盖）、middleware_net 网络容器名互连、状态接口就绪检查。须先在同一主机执行「部署 Elasticsearch」。",
		category:    "可视化",
		requires:    requiresDocker,
		variables:   `[{"name":"elastic_password","label":"elastic 密码","default":"","required":true},{"name":"kibana_password","label":"kibana_system 密码","default":"","required":true},{"name":"port","label":"端口","default":"5601","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/kibana","required":true},{"name":"image","label":"镜像","default":"docker.elastic.co/kibana/kibana:8.11.0","required":true}]`,
		path:        "builtin/install-kibana.sh",
		services:    `[{"name":"Kibana","url":"http://{{ip}}:{{port}}","web":true}]`,
	},
	{
		name:        "安装 Docker Registry",
		description: "docker compose 方式基于官方 registry:2 镜像部署私有镜像仓库（生成 compose.yml 落盘）：数据目录持久化（重建容器不丢数据）、可选 htpasswd 基础认证、开机自启与 /v2/ 健康检查，输出推送示例与客户端 insecure-registries 配置提示。依赖 Docker。",
		category:    "工具",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"监听端口","default":"5000","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/registry","required":true},{"name":"username","label":"认证用户名（留空免认证）","default":"","required":false},{"name":"password","label":"认证密码（启用认证时必填）","default":"","required":false}]`,
		services:    `[{"name":"Docker Registry","url":"http://{{ip}}:{{port}}","web":true}]`,
		path:        "builtin/install-registry.sh",
	},
	{
		name:        "部署 VictoriaMetrics",
		description: "以 docker compose 方式部署单机版 VictoriaMetrics 时序数据库（v1.150.0）：数据目录持久化、保留周期可调、healthcheck 自检并接入 middleware_net 共享网络。生成 compose.yml 落盘，后续改参数直接编辑后 compose up 即可平滑应用。输出 vmui 地址与 Prometheus remote_write 接入点。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"监听端口","default":"8428","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/victoriametrics","required":true},{"name":"retention","label":"数据保留（月）","default":"3","required":true},{"name":"image","label":"镜像","default":"victoriametrics/victoria-metrics:v1.150.0","required":true}]`,
		services:    `[{"name":"VictoriaMetrics vmui","url":"http://{{ip}}:{{port}}/vmui","web":true}]`,
		path:        "builtin/install-victoriametrics.sh",
	},
	{
		name:        "部署 Prometheus",
		description: "docker compose 部署 Prometheus 时序监控（生成 compose.yml 落盘）：TSDB 数据持久化、保留天数可调、/targets 状态自检并接入 middleware_net 共享网络。抓取目标走 file_sd（conf/targets/ 下编辑 10s 自动生效，适合对接自研采集器）；可选 remote_write 直推 VictoriaMetrics。生成 compose.yml 落盘，改参数编辑后 compose up 即可平滑应用。依赖 Docker。",
		category:    "监控",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"监听端口","default":"9090","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/prometheus","required":true},{"name":"retention_days","label":"数据保留（天）","default":"30","required":true},{"name":"remote_write_url","label":"remote_write 地址（如 VictoriaMetrics，可留空）","default":"","required":false},{"name":"image","label":"镜像","default":"prom/prometheus:v2.55.1","required":true}]`,
		services:    `[{"name":"Prometheus","url":"http://{{ip}}:{{port}}","web":true}]`,
		path:        "builtin/install-prometheus.sh",
	},
	{
		name:        "内核网络参数调优",
		description: "开启 BBR 拥塞控制并批量应用生产级内核/网络参数：覆盖 TCP 连接生命周期、连接队列与收发缓冲、TCP 特性、文件句柄/inotify/内存，配置与每项作用注释写入 /etc/sysctl.d/99-tuning.conf，逐条容错应用。",
		category:    "系统",
		variables:   `[]`,
		path:        "builtin/kernel-tuning.sh",
	},
	{
		name:        "部署 RocketMQ",
		description: "docker compose 部署 RocketMQ（namesrv + broker 双容器，生成 compose.yml 落盘）：broker.conf 自动指向主机 IP（brokerIP1）、middleware_net 共享网络、store 持久化、自动创建主题。内存受限主机建议仅单 broker。依赖 Docker。",
		category:    "消息队列",
		requires:    requiresDocker,
		variables:   `[{"name":"ns_port","label":"namesrv 端口","default":"9876","required":true},{"name":"broker_port","label":"broker 端口","default":"10911","required":true},{"name":"vip_port","label":"VIP 端口(10909)","default":"10909","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/rocketmq","required":true},{"name":"image","label":"镜像","default":"apache/rocketmq:4.9.7","required":true}]`,
		path:        "builtin/install-rocketmq.sh",
	},
	{
		name:        "部署 Kafka",
		description: "docker compose 部署 Kafka（bitnami KRaft 单节点，无需 Zookeeper，生成 compose.yml 落盘）：自动创建主题、middleware_net 共享网络、数据持久化。生产者/消费者用 {{__ip}}:{{port}} 接入。依赖 Docker。",
		category:    "消息队列",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"端口","default":"9092","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/kafka","required":true},{"name":"image","label":"镜像","default":"bitnami/kafka:3.6","required":true}]`,
		path:        "builtin/install-kafka.sh",
	},
	{
		name:        "部署 Consul",
		description: "docker compose 部署 Consul 单 agent（server + ui，生成 compose.yml 落盘）：middleware_net 共享网络、数据持久化。服务注册/发现用 http://{{__ip}}:{{port}}。依赖 Docker。",
		category:    "配置注册中心",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"HTTP 端口","default":"8500","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/consul","required":true},{"name":"image","label":"镜像","default":"hashicorp/consul:1.18","required":true}]`,
		services:    `[{"name":"Consul UI","url":"http://{{ip}}:{{port}}/ui","web":true}]`,
		path:        "builtin/install-consul.sh",
	},
	{
		name:        "部署 etcd",
		description: "docker compose 部署 etcd 单节点（生成 compose.yml 落盘）：middleware_net 共享网络、数据持久化。客户端 API v3 端口 2379，Peer 端口 2380。依赖 Docker。",
		category:    "配置注册中心",
		requires:    requiresDocker,
		variables:   `[{"name":"client_port","label":"客户端端口","default":"2379","required":true},{"name":"peer_port","label":"Peer 端口","default":"2380","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/etcd","required":true},{"name":"image","label":"镜像","default":"quay.io/coreos/etcd:v3.5.10","required":true}]`,
		path:        "builtin/install-etcd.sh",
	},
	{
		name:        "部署 Zookeeper",
		description: "docker compose 部署 Zookeeper 单节点（生成 compose.yml 落盘）：middleware_net 共享网络、data 与 datalog 分离持久化。客户端端口 2181。依赖 Docker。",
		category:    "配置注册中心",
		requires:    requiresDocker,
		variables:   `[{"name":"client_port","label":"客户端端口","default":"2181","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/zookeeper","required":true},{"name":"image","label":"镜像","default":"zookeeper:3.8","required":true}]`,
		services:    `[{"name":"Zookeeper","url":"zk://{{ip}}:{{client_port}}","web":false}]`,
		path:        "builtin/install-zookeeper.sh",
	},
	{
		name:        "部署 DragonflyDB",
		description: "docker compose 部署 DragonflyDB（Redis 兼容的高性能缓存数据库，生成 compose.yml 落盘）：密码认证、默认快照持久化、middleware_net 共享网络、数据目录持久化。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"端口","default":"6379","required":true},{"name":"password","label":"访问密码","default":"","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/dragonfly","required":true},{"name":"image","label":"镜像","default":"docker.dragonflydb.io/dragonflydb/dragonfly:v1.20.0","required":true}]`,
		services:    `[{"name":"DragonflyDB","url":"redis://{{ip}}:{{port}}","web":false}]`,
		path:        "builtin/install-dragonfly.sh",
	},
	{
		name:        "部署 ClickHouse",
		description: "docker compose 部署 ClickHouse 单节点（生成 compose.yml 落盘）：默认库/账号密码、middleware_net 共享网络、数据与日志目录持久化。HTTP 8123 / 原生 TCP 9000。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"http_port","label":"HTTP 端口","default":"8123","required":true},{"name":"tcp_port","label":"原生端口","default":"9000","required":true},{"name":"username","label":"账号","default":"default","required":true},{"name":"password","label":"密码","default":"","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/clickhouse","required":true},{"name":"image","label":"镜像","default":"clickhouse/clickhouse-server:23.11","required":true}]`,
		services:    `[{"name":"ClickHouse HTTP","url":"http://{{ip}}:{{http_port}}","web":true}]`,
		path:        "builtin/install-clickhouse.sh",
	},
	{
		name:        "部署 MinIO",
		description: "docker compose 部署 MinIO（S3 兼容对象存储，生成 compose.yml 落盘）：root 账号密码、middleware_net 共享网络、数据目录持久化。API 9000 / 控制台 9001。依赖 Docker。",
		category:    "工具",
		requires:    requiresDocker,
		variables:   `[{"name":"api_port","label":"API 端口","default":"9000","required":true},{"name":"console_port","label":"控制台端口","default":"9001","required":true},{"name":"root_user","label":"访问账号","default":"admin","required":true},{"name":"root_password","label":"访问密码（≥8位）","default":"","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/minio","required":true},{"name":"image","label":"镜像","default":"minio/minio:latest","required":true}]`,
		services:    `[{"name":"MinIO 控制台","url":"http://{{ip}}:{{console_port}}","web":true}]`,
		path:        "builtin/install-minio.sh",
	},
	{
		name:        "部署 Elasticsearch 集群节点",
		description: "部署 ES 9.x 集群的任一节点（引导/加入由 is_bootstrap 决定，生成 elasticsearch.yml 落盘，compose + middleware_net）：引导节点填 cluster.initial_master_nodes，加入节点仅靠 seed_hosts 单播并入。默认关闭 xpack 安全以简化局域网集群。编排：先 is_bootstrap=true 部署引导首节点，再把「首节点IP:transport」填入其余节点 seed_hosts 依次部署。依赖 Docker。",
		category:    "数据库",
		requires:    requiresDocker,
		variables:   `[{"name":"node_name","label":"节点名","default":"es-node-1","required":true},{"name":"roles","label":"节点角色(master,data,ingest 逗号分隔)","default":"master,data","required":true},{"name":"port","label":"HTTP 端口","default":"9200","required":true},{"name":"transport_port","label":"Transport 端口","default":"9300","required":true},{"name":"cluster_name","label":"集群名","default":"elasticsearch-cluster","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/es-cluster","required":true},{"name":"seed_hosts","label":"集群其他节点 IP:transport（JSON 列表文本，逗号分隔）","default":"","required":false},{"name":"is_bootstrap","label":"是否引导首节点(true/false)","default":"false","required":true},{"name":"java_opts","label":"JVM 内存","default":"-Xms1g -Xmx1g","required":true},{"name":"image","label":"镜像","default":"docker.elastic.co/elasticsearch/elasticsearch:9.5.3","required":true}]`,
		services:    `[{"name":"Elasticsearch 节点","url":"http://{{ip}}:{{port}}","web":true}]`,
		path:        "stacks/elasticsearch/scripts/node.sh",
	},
	{
		name:        "部署 RocketMQ NameServer",
		description: "部署 RocketMQ NameServer 节点（compose + middleware_net，data/logs 持久化）。可多台/多实例组成 namesrv 集群；部署后把全部 namesrv 的 ip:9876 汇总为逗号列表填入各 broker 的 namesrvAddr。依赖 Docker。",
		category:    "消息队列",
		requires:    requiresDocker,
		variables:   `[{"name":"ns_name","label":"namesrv 实例名","default":"namesrv1","required":true},{"name":"port","label":"监听端口","default":"9876","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/rocketmq-ns","required":true},{"name":"image","label":"镜像","default":"apache/rocketmq:4.9.7","required":true}]`,
		services:    `[]`,
		path:        "stacks/rocketmq/scripts/namesrv.sh",
	},
	{
		name:        "部署 RocketMQ Broker 节点",
		description: "部署 RocketMQ Broker 节点（host 网络，brokerIP1 自动取宿主机 IP）：broker_id=0 为 Master，同组(same broker_name) 非 0 为 Slave 组成主备。namesrvAddr 填集群全部 namesrv 的 ip:9876 逗号分隔。先部署 namesrv 与全部 broker。依赖 Docker。",
		category:    "消息队列",
		requires:    requiresDocker,
		variables:   `[{"name":"cluster_name","label":"集群名","default":"DefaultCluster","required":true},{"name":"broker_name","label":"broker 组名(主备同组同名)","default":"broker-a","required":true},{"name":"broker_id","label":"broker id(0=master,>0=slave)","default":"0","required":true},{"name":"broker_port","label":"broker 端口","default":"10911","required":true},{"name":"namesrv_addr","label":"namesrv 地址 ip:9876 逗号分隔","default":"","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/rocketmq-broker","required":true},{"name":"image","label":"镜像","default":"apache/rocketmq:4.9.7","required":true}]`,
		services:    `[]`,
		path:        "stacks/rocketmq/scripts/broker.sh",
	},
	{
		name:        "部署 RabbitMQ 集群节点",
		description: "部署 RabbitMQ 集群节点（compose + middleware_net，统一 RABBITMQ_ERLANG_COOKIE、hostname）：is_bootstrap=true 初始化首节点；false 时就绪后自动 rabbitmqctl join_cluster 加入，失败自动恢复独立并提示排查。编译：先首节点，其余填 join_cluster_host=rabbit@首节点名。依赖 Docker。",
		category:    "消息队列",
		requires:    requiresDocker,
		variables:   `[{"name":"node_name","label":"节点名","default":"rabbit-node-1","required":true},{"name":"amqp_port","label":"AMQP 端口","default":"5672","required":true},{"name":"mgmt_port","label":"管理端口","default":"15672","required":true},{"name":"erlang_cookie","label":"Erlang cookie(全节点必须一致)","default":"","required":true},{"name":"admin_user","label":"管理账号","default":"admin","required":false},{"name":"admin_pass","label":"管理密码","default":"","required":true},{"name":"home_dir","label":"服务主目录","default":"/data/rabbitmq","required":true},{"name":"is_bootstrap","label":"是否集群首节点(true/false)","default":"false","required":true},{"name":"join_cluster_host","label":"加入目标节点 rabbit@host","default":"","required":false},{"name":"image","label":"镜像","default":"rabbitmq:3.13-management","required":true}]`,
		services:    `[{"name":"RabbitMQ 管理台","url":"http://{{ip}}:{{mgmt_port}}","web":true}]`,
		path:        "stacks/rabbitmq/scripts/node.sh",
	},
}

// loadBuiltinScript 读取模板脚本并注入其引用的资源占位符。
func loadBuiltinScript(t builtinTemplate) (string, error) {
	b, err := builtinFS.ReadFile(t.path)
	if err != nil {
		return "", err
	}
	script := string(b)
	if script == "" {
		return "", errors.New("empty builtin script: " + t.path)
	}
	for key, asset := range t.assets {
		c, err := builtinFS.ReadFile(asset)
		if err != nil {
			return "", err
		}
		script = strings.ReplaceAll(script, "@@"+key+"@@", string(c))
	}
	return script, nil
}

// seedBuiltinTemplates 预置内置模板（幂等，按 name 去重）。
// 内置模板随版本演进：脚本有变化时原地刷新（内置模板 UI 只读，不存在覆盖用户修改的问题）；
// 已不在当前内置列表中的历史内置模板会被清理。
func seedBuiltinTemplates(db *sql.DB) error {
	scripts := make(map[string]string, len(builtinTemplates))
	for _, t := range builtinTemplates {
		s, err := loadBuiltinScript(t)
		if err != nil {
			return err
		}
		scripts[t.name] = s
	}

	// 清理历史版本遗留的内置模板。
	// 已被部署任务/定时任务引用的模板受外键约束不可硬删，降级为普通模板保留（历史可追溯）；
	// 无引用的直接删除。
	names := make([]string, len(builtinTemplates))
	args := make([]interface{}, len(builtinTemplates))
	for i, t := range builtinTemplates {
		names[i] = t.name
		args[i] = t.name
	}
	if _, err := db.Exec(
		`UPDATE deploy_templates SET is_builtin=0
		WHERE is_builtin=1 AND name NOT IN (`+strings.Repeat("?,", len(args)-1)+`?)
		AND (EXISTS(SELECT 1 FROM deploy_tasks t WHERE t.template_id=deploy_templates.id)
		  OR EXISTS(SELECT 1 FROM deploy_schedules s WHERE s.template_id=deploy_templates.id))`, args...,
	); err != nil {
		return err
	}
	if _, err := db.Exec(
		`DELETE FROM deploy_templates WHERE is_builtin=1 AND name NOT IN (`+strings.Repeat("?,", len(args)-1)+"?)", args...,
	); err != nil {
		return err
	}

	for _, t := range builtinTemplates {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM deploy_templates WHERE name=?`, t.name).Scan(&exists); err != nil {
			return err
		}
		script := scripts[t.name]
		// services/requires 为空的模板统一落 '[]'：空字符串会使 json.RawMessage 序列化失败，导致列表接口 500
		services := t.services
		if services == "" {
			services = "[]"
		}
		requires := t.requires
		if requires == "" {
			requires = "[]"
		}
		configs := t.configs
		if configs == "" {
			configs = "[]"
		}
		if exists > 0 {
			var curScript, curVars, curServices, curRequires, curConfigs, curCat string
			if err := db.QueryRow(`SELECT script, variables, services, requires, configs, category FROM deploy_templates WHERE name=? AND is_builtin=1`, t.name).Scan(&curScript, &curVars, &curServices, &curRequires, &curConfigs, &curCat); err == nil && (curScript != script || curVars != t.variables || curServices != services || curRequires != requires || curConfigs != configs || curCat != t.category) {
				if _, err := db.Exec(
					`UPDATE deploy_templates SET description=?, script=?, variables=?, services=?, requires=?, configs=?, category=?, updated_at=datetime('now','localtime') WHERE name=? AND is_builtin=1`,
					t.description, script, t.variables, services, requires, configs, t.category, t.name,
				); err != nil {
					return err
				}
			}
			continue
		}
		_, err := db.Exec(
			`INSERT INTO deploy_templates(name, description, script, variables, services, requires, configs, category, is_builtin) VALUES(?,?,?,?,?,?,?,?,1)`,
			t.name, t.description, script, t.variables, services, requires, configs, t.category,
		)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	return nil
}
