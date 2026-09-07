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

//go:embed builtin
var builtinFS embed.FS

type builtinTemplate struct {
	name        string
	description string
	category    string            // 功能分类：系统/工具/中间件/监控…
	variables   string            // JSON: [{name,label,default,required}]
	services    string            // JSON: [{name,url,web}]
	requires    string            // JSON: [{check,hint}] 前置依赖检查
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
		description: "Docker 部署 MySQL 单机：utf8mb4/生产参数 my.cnf（已有则备份）、middleware_net 共享网络、数据目录持久化、等待就绪。root 密码仅首次初始化数据时生效。依赖 Docker。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"root_password","label":"root 密码","default":"","required":true},{"name":"port","label":"端口","default":"3306","required":true},{"name":"data_dir","label":"数据目录","default":"/data/middleware/mysql","required":true},{"name":"image","label":"镜像","default":"mysql:8.0","required":true}]`,
		path:        "builtin/install-mysql.sh",
		services:    `[{"name":"MySQL","url":"mysql://{{ip}}:{{port}}","web":false}]`,
	},
	{
		name:        "部署 Redis",
		description: "Docker 部署 Redis：密码认证、AOF 持久化、middleware_net 共享网络、数据目录持久化、健康检查。依赖 Docker。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"password","label":"访问密码","default":"","required":true},{"name":"port","label":"端口","default":"6379","required":true},{"name":"data_dir","label":"数据目录","default":"/data/middleware/redis","required":true},{"name":"image","label":"镜像","default":"redis:7","required":true}]`,
		path:        "builtin/install-redis.sh",
		services:    `[{"name":"Redis","url":"redis://{{ip}}:{{port}}","web":false}]`,
	},
	{
		name:        "部署 MongoDB",
		description: "Docker 部署 MongoDB：自定义 mongod.conf（已有则备份）、middleware_net 共享网络、数据目录持久化，首次自动创建管理员用户（已存在则跳过）。依赖 Docker。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"admin_username","label":"管理员用户名","default":"admin","required":true},{"name":"admin_password","label":"管理员密码","default":"","required":true},{"name":"port","label":"端口","default":"27017","required":true},{"name":"data_dir","label":"数据目录","default":"/data/middleware/mongodb","required":true},{"name":"image","label":"镜像","default":"mongo:6.0","required":true}]`,
		path:        "builtin/install-mongo.sh",
		services:    `[{"name":"MongoDB","url":"mongodb://{{ip}}:{{port}}","web":false}]`,
	},
	{
		name:        "部署 RabbitMQ",
		description: "Docker 部署 RabbitMQ（management 版）：middleware_net 共享网络、可选延迟队列插件（GitHub 官方源下载，多加速地址轮换重试，已存在则跳过）、插件启用状态校验。依赖 Docker。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"username","label":"管理员用户名","default":"root","required":true},{"name":"password","label":"管理员密码","default":"","required":true},{"name":"amqp_port","label":"AMQP 端口","default":"5672","required":true},{"name":"mgmt_port","label":"管理界面端口","default":"15672","required":true},{"name":"delayed_plugin","label":"安装延迟队列插件(yes/no)","default":"yes","required":true},{"name":"image","label":"镜像","default":"rabbitmq:3.11.15-management","required":true}]`,
		path:        "builtin/install-rabbitmq.sh",
		services:    `[{"name":"RabbitMQ 管理","url":"http://{{ip}}:{{mgmt_port}}","web":true}]`,
	},
	{
		name:        "部署 Nacos",
		description: "Docker 部署 Nacos 单机版（MySQL 存储）：自动初始化 nacos 数据库与表结构（已初始化则跳过）、middleware_net 网络容器名互连、控制台就绪检查。须先在同一主机执行「部署 MySQL」。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"db_username","label":"数据库用户名","default":"nacos","required":true},{"name":"db_password","label":"数据库密码","default":"","required":true},{"name":"port","label":"控制台端口","default":"8848","required":true},{"name":"grpc_port","label":"gRPC 端口","default":"9848","required":true},{"name":"image","label":"镜像","default":"nacos/nacos-server:v2.1.0","required":true}]`,
		path:        "builtin/install-nacos.sh",
		services:    `[{"name":"Nacos 控制台","url":"http://{{ip}}:{{port}}/nacos/","web":true}]`,
		assets:      map[string]string{"NACOS_SQL": "builtin/nacos-init.sql"},
	},
	{
		name:        "部署 Elasticsearch",
		description: "Docker 部署 Elasticsearch 单机（开启安全认证、关闭 HTTPS）：数据目录自动授权 uid=1000、Docker 原生健康检查、JVM 内存可调。elastic 密码仅首次初始化数据时生效。依赖 Docker。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"elastic_password","label":"elastic 密码","default":"","required":true},{"name":"port","label":"端口","default":"9200","required":true},{"name":"data_dir","label":"数据目录","default":"/data/middleware/elasticsearch","required":true},{"name":"java_opts","label":"JVM 内存","default":"-Xms512m -Xmx512m","required":true},{"name":"image","label":"镜像","default":"docker.elastic.co/elasticsearch/elasticsearch:8.11.0","required":true}]`,
		path:        "builtin/install-elasticsearch.sh",
		services:    `[{"name":"Elasticsearch","url":"http://{{ip}}:{{port}}","web":true}]`,
	},
	{
		name:        "部署 Kibana",
		description: "Docker 部署 Kibana：自动通过 ES API 设置 kibana_system 密码（幂等覆盖）、middleware_net 网络容器名互连、状态接口就绪检查。须先在同一主机执行「部署 Elasticsearch」。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"elastic_password","label":"elastic 密码","default":"","required":true},{"name":"kibana_password","label":"kibana_system 密码","default":"","required":true},{"name":"port","label":"端口","default":"5601","required":true},{"name":"data_dir","label":"数据目录","default":"/data/middleware/kibana","required":true},{"name":"image","label":"镜像","default":"docker.elastic.co/kibana/kibana:8.11.0","required":true}]`,
		path:        "builtin/install-kibana.sh",
		services:    `[{"name":"Kibana","url":"http://{{ip}}:{{port}}","web":true}]`,
	},
	{
		name:        "安装 Docker Registry",
		description: "基于官方 registry:2 镜像部署私有镜像仓库：数据目录持久化（重建容器不丢数据）、可选 htpasswd 基础认证、开机自启与 /v2/ 健康检查，输出推送示例与客户端 insecure-registries 配置提示。依赖 Docker。",
		category:    "中间件",
		requires:    requiresDocker,
		variables:   `[{"name":"port","label":"监听端口","default":"5000","required":true},{"name":"data_dir","label":"数据目录","default":"/data/registry","required":true},{"name":"username","label":"认证用户名（留空免认证）","default":"","required":false},{"name":"password","label":"认证密码（启用认证时必填）","default":"","required":false}]`,
		services:    `[{"name":"Docker Registry","url":"http://{{ip}}:{{port}}","web":true}]`,
		path:        "builtin/install-registry.sh",
	},
	{
		name:        "内核网络参数调优",
		description: "开启 BBR 拥塞控制并批量应用生产级内核/网络参数：覆盖 TCP 连接生命周期、连接队列与收发缓冲、TCP 特性、文件句柄/inotify/内存，配置与每项作用注释写入 /etc/sysctl.d/99-tuning.conf，逐条容错应用。",
		category:    "系统",
		variables:   `[]`,
		path:        "builtin/kernel-tuning.sh",
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
		if exists > 0 {
			var curScript, curVars, curServices, curRequires, curCat string
			if err := db.QueryRow(`SELECT script, variables, services, requires, category FROM deploy_templates WHERE name=? AND is_builtin=1`, t.name).Scan(&curScript, &curVars, &curServices, &curRequires, &curCat); err == nil && (curScript != script || curVars != t.variables || curServices != services || curRequires != requires || curCat != t.category) {
				if _, err := db.Exec(
					`UPDATE deploy_templates SET description=?, script=?, variables=?, services=?, requires=?, category=?, updated_at=datetime('now','localtime') WHERE name=? AND is_builtin=1`,
					t.description, script, t.variables, services, requires, t.category, t.name,
				); err != nil {
					return err
				}
			}
			continue
		}
		_, err := db.Exec(
			`INSERT INTO deploy_templates(name, description, script, variables, services, requires, category, is_builtin) VALUES(?,?,?,?,?,?,?,1)`,
			t.name, t.description, script, t.variables, services, requires, t.category,
		)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	return nil
}
