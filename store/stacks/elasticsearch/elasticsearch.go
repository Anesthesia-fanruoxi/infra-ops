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
// reset 标 FullOnly 故扩容/加装时绝不清空既有数据；masters/coords/data 标 Component 供
// 加装裁剪与运行视图芯片状态聚合（verify 校验阶段无组件产出，与 bigdata 口径一致不标）。
// 集群模式无流水线（nil = 传统单步流程）。
func (d *Driver) Pipeline(mode string) []model.StackPhase {
	if mode != "cold_warm_hot" {
		return nil
	}
	return []model.StackPhase{
		{Key: "reset", Label: "清理旧环境与残留容器", Target: "all", FullOnly: true},
		{Key: "masters", Label: "启动 master 候选节点", Target: "all", Component: "elasticsearch"},
		{Key: "coords", Label: "启动纯协调节点", Target: "all", Component: "elasticsearch"},
		{Key: "data", Label: "启动数据节点（hot/warm/cold）", Target: "all", Component: "elasticsearch"},
		{Key: "verify", Label: "校验集群健康与各分层", Target: "leader", Always: true},
	}
}

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入，字段与顺序零变更）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
		Key:            "elasticsearch",
		Name:           "Elasticsearch",
		Category:       "service",
		Description:    "一次成型部署 Elasticsearch 集群。双模式：原「集群」模式（单容器，指定一台引导主节点）；「冷热温」模式（一机多容器：矩阵勾选即部署，每格一个对应角色的 ES 容器，数据层与 master/协调互斥，master 与协调可同机多容器；对外仅暴露协调/master 入口）。host 网络直连，xpack 安全保持关闭。规格由三档内部预设决定（小额尝鲜 / 标准使用 / 大力出奇迹），不需要逐节点填 JVM 参数；档位只定义每角色 JVM 参数与参考数量，机器数量与硬件由用户自定。",
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
				Description: "一机多容器：矩阵勾选即部署，每格一个对应角色的 ES 容器（master 与协调可同机叠加，数据层 hot/warm/cold 可叠加；数据层与 master/协调互斥）。行清空自动归冷热温数据层；端口按角色固定偏移（master 9200/协调 9210/hot 9220/warm 9230/cold 9240），对外仅登记协调或 master 入口。",
				MinHosts:    4, HostHint: "至少 4 台；建议 ≥3 master（奇数）+ ≥2 纯协调 + 数据层（按 tier）",
				HasBootstrap: true, DefaultHomeDir: "/data/elasticsearch",
			},
		},
		// 规格档位表（三档内部预设）：随蓝图下发给套件自己的第 2 步组件渲染，
		// 与部署期堆内存注入同源（sizing.go 是唯一事实源，前端不再重复维护数字）。
		Extras: map[string]any{"sizing": esSizingTable()},
		SharedVars: []model.StackVar{
			{Name: "cluster_name", Label: "集群名", Default: "es-cluster", Required: true},
			{Name: "image", Label: "镜像", Default: "elasticsearch:9.5.3", Required: true},
			// 规格档位：mini(小额尝鲜) / standard(标准使用) / full(大力出奇迹)，两模式通用。
			// 由套件自己的第 2 步组件选择（第 3 步经 visibleSharedVars 过滤，不重复展示）；
			// JVM 堆与内存建议由 sizing.go 按「档位 × 角色」给出，用户不再手填 JVM 参数。
			{Name: "sizing", Label: "规格档位", Default: esSizingStandard, Required: true},
			// 冷热温多容器：端口按角色固定偏移（9200/9210/9220/9230/9240，transport +100），
			// transport_port 仅集群模式可填；冷热温对外只登记协调/master 入口。
			// （原 java_opts / heap_xms / heap_xmx / jvm_opts 四个 JVM 变量已由 sizing 档位取代。）
			{Name: "transport_port", Label: "Transport 端口", Default: "9300", Required: true, Modes: []string{"cluster"}},
			// 自建镜像仓库（选填）：前端渲染「自建仓库」下拉，与部署中心 hub 镜像源同源；
			// 选中后平台统一预热并改写全部镜像参数（引擎 privatizeImages 消费本键）
			{Name: "image_registry", Label: "镜像仓库（自建，选填）", Type: "registry", Default: ""},
		},
		HostVars: []model.StackVar{
			{Name: "port", Label: "HTTP 端口", Default: "9200", Required: true, Modes: []string{"cluster"}},
			{Name: "home_dir", Label: "服务主目录", Default: "/data/elasticsearch", Required: true},
			// 冷热温：每台主机的角色勾选（逗号分隔 master/coordinator/data_hot,data_warm,data_cold），
			// 每个勾选角色 = 该主机一个独立 ES 容器；空值由后端兜底为冷热温全数据层。
			{Name: "roles", Label: "节点角色", Default: "", Required: false, Modes: []string{"cold_warm_hot"}},
		},
	}
}
