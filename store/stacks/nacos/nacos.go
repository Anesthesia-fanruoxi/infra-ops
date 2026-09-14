// Package nacos Nacos 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包。
package nacos

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver Nacos 驱动（单模式 cluster）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "nacos" }

// Phase 阶段脚本：cluster 一枚 node 脚本（内联注入建库 SQL 素材）。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if phase != stackkit.PhaseNode || mode != "cluster" {
		return stackkit.PhaseFile{}, false
	}
	return stackkit.PhaseFile{
		Path: "stacks/nacos/scripts/node.sh",
		Assets: map[string]string{
			"NACOS_SQL": "stacks/nacos/configs/nacos-init.sql",
		},
	}, true
}

// Pipeline Nacos 无多阶段流水线（空 = 走传统 node→bootstrap 单步流程）。
func (d *Driver) Pipeline(string) []model.StackPhase { return nil }

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
	}
}
