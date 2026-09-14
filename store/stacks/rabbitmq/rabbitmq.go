// Package rabbitmq RabbitMQ 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包。
package rabbitmq

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver RabbitMQ 驱动（单模式 cluster）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "rabbitmq" }

// Phase 阶段脚本：cluster 一枚 node 脚本（注入 rabbitmq.conf 素材）。
//
// 注：该 node.sh 同时被「基础建设 · 部署 RabbitMQ」模板直接复用，
// 模板侧经 store.readAsset 前缀路由读到同一份资源，不重复内嵌。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if phase != stackkit.PhaseNode || mode != "cluster" {
		return stackkit.PhaseFile{}, false
	}
	return stackkit.PhaseFile{
		Path: "stacks/rabbitmq/scripts/node.sh",
		Assets: map[string]string{
			"RABBITMQ_CONF": "stacks/rabbitmq/configs/rabbitmq.conf",
		},
	}, true
}

// Pipeline RabbitMQ 无多阶段流水线（空 = 走传统 node→bootstrap 单步流程）。
func (d *Driver) Pipeline(string) []model.StackPhase { return nil }

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
	}
}
