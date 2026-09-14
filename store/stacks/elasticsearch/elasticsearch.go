// Package elasticsearch Elasticsearch 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包
// （否则与 store/registry.go 形成循环）；套件之间也不互相 import。
package elasticsearch

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver Elasticsearch 驱动（集群 / 冷热温双模式）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "elasticsearch" }

// Phase 阶段脚本：集群模式一枚 node 脚本；冷热温模式另有 bootstrap / scale_in。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	switch phase {
	case stackkit.PhaseNode:
		switch mode {
		case "cluster":
			return stackkit.PhaseFile{
				Path: "stacks/elasticsearch/scripts/node.sh",
				Assets: map[string]string{
					"ES_MASTER_YML": "stacks/elasticsearch/configs/elasticsearch-master.yml",
					"ES_MEMBER_YML": "stacks/elasticsearch/configs/elasticsearch-member.yml",
				},
			}, true
		case "cold_warm_hot":
			// 角色动态（node.roles/初识 master/SSL 条件块），elasticsearch.yml 由脚本内联生成，不走静态素材
			return stackkit.PhaseFile{Path: "stacks/elasticsearch/scripts/cold-warm-hot/node.sh"}, true
		}
	case stackkit.PhaseBootstrap:
		if mode == "cold_warm_hot" {
			return stackkit.PhaseFile{Path: "stacks/elasticsearch/scripts/cold-warm-hot/bootstrap.sh"}, true
		}
	case stackkit.PhaseScaleIn:
		if mode == "cold_warm_hot" {
			return stackkit.PhaseFile{Path: "stacks/elasticsearch/scripts/cold-warm-hot/scale_in.sh"}, true
		}
	}
	return stackkit.PhaseFile{}, false
}

// Pipeline 冷热温模式主脑流水线：reset（清残留）→ masters → coords → data → verify（leader 校验）。
// reset 标 FullOnly 故扩容/加装时绝不清空既有数据。集群模式无流水线（nil = 传统单步流程）。
func (d *Driver) Pipeline(mode string) []model.StackPhase {
	if mode != "cold_warm_hot" {
		return nil
	}
	return []model.StackPhase{
		{Key: "reset", Label: "清理旧环境与残留容器", Target: "all", FullOnly: true},
		{Key: "masters", Label: "启动 master 候选节点", Target: "all"},
		{Key: "coords", Label: "启动纯协调节点", Target: "all"},
		{Key: "data", Label: "启动数据节点（hot/warm/cold）", Target: "all"},
		{Key: "verify", Label: "校验集群健康与各分层", Target: "leader", Always: true},
	}
}

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入，字段与顺序零变更）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
	}
}
