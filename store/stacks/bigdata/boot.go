package bigdata

import (
	"strings"

	"infra-ops/model"
)

// boot.go：引导（bootstrap）主机解析（原 api/stack/stack_instance_build.go /
// stack_instance_reinstall.go 的 bigdata 分支原样迁入）。
//
// 引擎通用规则把「主节点」标为引导机；但大数据底座的集群初始化是 HDFS format/启动
// NameNode，必须落在 NameNode 所在主机上——角色规划下它可能与主节点分离。

// BootstrapHostIP 实现 stackkit.BootstrapHostResolver：返回 NameNode 主机 IP。
// 未解析到 NameNode 时返回空串，引擎沿用通用规则。
func (d *Driver) BootstrapHostIP(hosts []model.StackRunHost) string {
	return strings.TrimSpace(Masters(hosts)["hdfs"])
}
