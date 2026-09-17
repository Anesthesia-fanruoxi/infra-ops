// Package redis Redis 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包
// （否则与 store/registry.go 形成循环）；套件之间也不互相 import。
package redis

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver Redis 驱动（主从 / 哨兵 / 集群三模式）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "redis" }

// Phase 阶段脚本：三模式各一枚 node 脚本；集群模式另有 bootstrap（CLUSTER CREATE）、
// 扩容加槽（cluster-add.sh）与缩容软下线（cluster-del.sh）。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	switch phase {
	case stackkit.PhaseNode:
		switch mode {
		case "replication":
			return stackkit.PhaseFile{
				Path: "stacks/redis/scripts/repl-node.sh",
				Assets: map[string]string{
					"REDIS_MASTER_CONF":  "stacks/redis/configs/repl-master.conf",
					"REDIS_REPLICA_CONF": "stacks/redis/configs/repl-replica.conf",
				},
			}, true
		case "sentinel":
			return stackkit.PhaseFile{
				Path: "stacks/redis/scripts/sentinel-node.sh",
				Assets: map[string]string{
					"REDIS_SENTINEL_MASTER_CONF":  "stacks/redis/configs/sentinel-master.conf",
					"REDIS_SENTINEL_REPLICA_CONF": "stacks/redis/configs/sentinel-replica.conf",
					"REDIS_SENTINEL_CONF":         "stacks/redis/configs/sentinel.conf",
				},
			}, true
		case "cluster":
			return stackkit.PhaseFile{
				Path: "stacks/redis/scripts/cluster-node.sh",
				Assets: map[string]string{
					"REDIS_CLUSTER_CONF": "stacks/redis/configs/cluster.conf",
				},
			}, true
		}
	case stackkit.PhaseBootstrap:
		if mode == "cluster" {
			return stackkit.PhaseFile{Path: "stacks/redis/scripts/cluster-bootstrap.sh"}, true
		}
	case stackkit.PhaseScaleOut:
		if mode == "cluster" {
			return stackkit.PhaseFile{Path: "stacks/redis/scripts/cluster-add.sh"}, true
		}
	case stackkit.PhaseScaleIn:
		// 集群缩容软下线：在存活成员上先把待移除节点的槽迁走、再 del-node，
		// 之后主流程才停容器。缺此脚本时引擎降级为「直接停服」，
		// 会让哈希槽指向已消失的节点（cluster_state 可能变 fail）。
		if mode == "cluster" {
			return stackkit.PhaseFile{Path: "stacks/redis/scripts/cluster-del.sh"}, true
		}
	}
	return stackkit.PhaseFile{}, false
}

// Pipeline Redis 无多阶段流水线（空 = 走传统 node→bootstrap 单步流程）。
func (d *Driver) Pipeline(string) []model.StackPhase { return nil }

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入，字段与顺序零变更）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
			// 哨兵主名参数化（默认与旧值一致）：同主机部署第二个哨兵实例时，
			// 两套哨兵若共用 mymaster 会互相干扰监控对象，改成不同主名即可隔离。
			{Name: "master_name", Label: "哨兵主名", Default: "mymaster", Required: true, Modes: []string{"sentinel"}},
			// 自建镜像仓库（选填）：前端渲染「自建仓库」下拉，与部署中心 hub 镜像源同源；
			// 选中后平台统一预热并改写全部镜像参数（引擎 privatizeImages 消费本键）
			{Name: "image_registry", Label: "镜像仓库（自建，选填）", Type: "registry", Default: ""},
		},
		HostVars: []model.StackVar{
			{Name: "port", Label: "端口", Default: "6379", Required: true},
			{Name: "home_dir", Label: "服务主目录", Default: "/data/redis", Required: true},
		},
	}
}
