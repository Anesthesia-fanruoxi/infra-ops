// Package kafka Kafka 套件的执行契约：蓝图、阶段脚本、角色规划。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包
// （否则与 store/registry.go 形成循环）；套件之间也不互相 import。
package kafka

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver Kafka 驱动（KRaft / ZooKeeper 双模式）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "kafka" }

// Phase 阶段脚本：两模式各一枚 node 脚本，无 bootstrap / 扩缩容脚本。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if phase != stackkit.PhaseNode {
		return stackkit.PhaseFile{}, false
	}
	switch mode {
	case "kraft":
		return stackkit.PhaseFile{Path: "stacks/kafka/scripts/kraft-node.sh"}, true
	case "zk":
		return stackkit.PhaseFile{Path: "stacks/kafka/scripts/zk-node.sh"}, true
	}
	return stackkit.PhaseFile{}, false
}

// Pipeline Kafka 无多阶段流水线（空 = 走传统 node→bootstrap 单步流程）。
func (d *Driver) Pipeline(string) []model.StackPhase { return nil }

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入，字段与顺序零变更）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
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
	}
}
