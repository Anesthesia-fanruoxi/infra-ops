// Package elfk ELFK 日志套件的执行契约：蓝图、阶段脚本、角色规划。
//
// ELFK 是「组件编排型」套件（非主从型）：ES cluster + Kibana 随引导机 + Logstash 单点 + Filebeat 全员。
//
// 依赖方向（铁律）：只依赖 model 与 store/stackkit，绝不 import store 包
// （否则与 store/registry.go 形成循环）；套件之间也不互相 import。
package elfk

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// Driver ELFK 驱动（标准编排单模式）。
type Driver struct{}

// New 建驱动（装配表登记用）。
func New() *Driver { return &Driver{} }

// Key 套件唯一键（与蓝图 key 一致）。
func (d *Driver) Key() string { return "elfk" }

// Phase 阶段脚本：standard 一枚 node 脚本；verify 阶段走 Target=leader →
// runPhaseBootstrap → LoadPhase(mode, "bootstrap")，ELFK 不需要集群初始化，
// bootstrapByMode 承载链路校验脚本（verify.sh）。
func (d *Driver) Phase(mode, phase string) (stackkit.PhaseFile, bool) {
	if mode != "standard" {
		return stackkit.PhaseFile{}, false
	}
	switch phase {
	case stackkit.PhaseNode:
		return stackkit.PhaseFile{
			Path: "stacks/elfk/scripts/node.sh",
			Assets: map[string]string{
				"ES_MASTER_YML": "stacks/elasticsearch/configs/elasticsearch-master.yml",
				"ES_MEMBER_YML": "stacks/elasticsearch/configs/elasticsearch-member.yml",
			},
		}, true
	case stackkit.PhaseBootstrap:
		return stackkit.PhaseFile{Path: "stacks/elfk/scripts/verify.sh"}, true
	}
	return stackkit.PhaseFile{}, false
}

// Pipeline 标准编排流水线：reset → es → kibana → logstash → filebeat → verify（leader 校验）。
func (d *Driver) Pipeline(mode string) []model.StackPhase {
	if mode != "standard" {
		return nil
	}
	return []model.StackPhase{
		{Key: "reset", Label: "清理旧环境与残留容器", Target: "all", Component: "elasticsearch,kibana,logstash,filebeat", FullOnly: true},
		{Key: "es", Label: "部署 Elasticsearch 节点", Target: "all", Component: "elasticsearch"},
		{Key: "kibana", Label: "部署 Kibana（引导机）", Target: "all", Component: "kibana"},
		{Key: "logstash", Label: "部署 Logstash（日志管道）", Target: "all", Component: "logstash"},
		{Key: "filebeat", Label: "部署 Filebeat 采集代理（全员）", Target: "all", Component: "filebeat"},
		{Key: "verify", Label: "校验 ELFK 链路", Target: "leader", Always: true},
	}
}

// Blueprint 套件蓝图（原 store/builtin_stacks.go 字面量原样迁入，字段与顺序零变更）。
func (d *Driver) Blueprint() model.StackBlueprint {
	return model.StackBlueprint{
		Key:            "elfk",
		Name:           "ELFK 日志套件",
		Category:       "platform",
		Description:    "一体化部署 Elasticsearch + Logstash + Filebeat + Kibana：ES 为 cluster 拓扑（1 引导主节点 + N 数据节点），Kibana 随引导机部署，Logstash 默认落第 2 台（可用 logstash_host 指定），Filebeat 部署在全部成员上采集容器与系统日志。",
		RequiresDocker: true,
		Modes: []model.StackMode{
			{
				Key: "standard", Label: "标准编排",
				Description: "ES 引导主节点 + N 数据节点；Kibana 自动随引导机；Logstash 单点（默认第 2 台）；Filebeat 全员。",
				MinHosts:    3, HostHint: "至少 3 台（建议 5 台：1 引导 ES+Kibana、1 Logstash、其余 ES 数据节点），并指定一台引导主节点",
				AssignMaster: true, DefaultHomeDir: "/data/elfk/es",
			},
		},
		SharedVars: []model.StackVar{
			{Name: "cluster_name", Label: "ES 集群名", Default: "elfk-cluster", Required: true},
			{Name: "image_es", Label: "Elasticsearch 镜像", Default: "elasticsearch:9.5.3", Required: true},
			{Name: "image_kibana", Label: "Kibana 镜像", Default: "kibana:9.5.3", Required: true},
			{Name: "image_logstash", Label: "Logstash 镜像", Default: "logstash:9.5.3", Required: true},
			{Name: "image_filebeat", Label: "Filebeat 镜像", Default: "filebeat:9.5.3", Required: true},
			{Name: "kibana_port", Label: "Kibana 端口", Default: "5601", Required: true},
			{Name: "transport_port", Label: "ES Transport 端口", Default: "9300", Required: true},
			{Name: "logstash_beats_port", Label: "Logstash Beats 端口", Default: "5044", Required: true},
			{Name: "logstash_host", Label: "Logstash 所在主机 IP（留空自动落第 2 台）", Default: ""},
			{Name: "es_heap", Label: "ES JVM 堆", Default: "-Xms1g -Xmx1g", Required: true},
		},
		HostVars: []model.StackVar{
			{Name: "port", Label: "ES HTTP 端口", Default: "9200", Required: true},
			{Name: "home_dir", Label: "ES 数据目录", Default: "/data/elfk/es", Required: true},
		},
	}
}
