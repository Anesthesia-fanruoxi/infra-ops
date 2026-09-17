// Package powerjob PowerJob 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包。
package powerjob

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver PowerJob 驱动（单模式 cluster）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "powerjob" }

// Phase 阶段脚本：cluster 一枚 node 脚本，无注入资源。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if phase != stackkit.PhaseNode || mode != "cluster" {
		return stackkit.PhaseFile{}, false
	}
	return stackkit.PhaseFile{Path: "stacks/powerjob/scripts/node.sh"}, true
}

// Pipeline PowerJob 无多阶段流水线（空 = 走传统 node→bootstrap 单步流程）。
func (d *Driver) Pipeline(string) []model.StackPhase { return nil }

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
			// 自建镜像仓库（选填）：前端渲染「自建仓库」下拉，与部署中心 hub 镜像源同源；
			// 选中后平台统一预热并改写全部镜像参数（引擎 privatizeImages 消费本键）
			{Name: "image_registry", Label: "镜像仓库（自建，选填）", Type: "registry", Default: ""},
		},
		HostVars: []model.StackVar{
			{Name: "server_port", Label: "控制台端口", Default: "7700", Required: true},
			{Name: "home_dir", Label: "服务主目录", Default: "/data/powerjob", Required: true},
		},
	}
}
