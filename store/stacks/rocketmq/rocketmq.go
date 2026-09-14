// Package rocketmq RocketMQ 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包。
package rocketmq

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver RocketMQ 驱动（单模式「一步成型」cluster）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "rocketmq" }

// Phase 阶段脚本：cluster 一枚 node 脚本（注入主/备 broker.conf 素材）。
//
// 注：namesrv.sh / broker.sh 同时被「基础建设」模板直接复用，走 store.readAsset 读取。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if phase != stackkit.PhaseNode || mode != "cluster" {
		return stackkit.PhaseFile{}, false
	}
	return stackkit.PhaseFile{
		Path: "stacks/rocketmq/scripts/node.sh",
		Assets: map[string]string{
			"BROKER_MASTER_CONF": "stacks/rocketmq/configs/broker-master.conf",
			"BROKER_SLAVE_CONF":  "stacks/rocketmq/configs/broker-slave.conf",
		},
	}, true
}

// Pipeline RocketMQ 无多阶段流水线（空 = 走传统 node→bootstrap 单步流程）。
func (d *Driver) Pipeline(string) []model.StackPhase { return nil }

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
	}
}
