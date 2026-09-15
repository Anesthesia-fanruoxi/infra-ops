// 离线资产声明：实现 stackkit.AssetProvisioner（可选能力，引擎经 SFTP 分发）。
package bigdata

import (
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// mysqlConnectorJar Hive HA 的 MySQL 驱动。apache/hive:4.0.0 官方镜像不含 Connector/J，
// metastore 连 MySQL 必需；ha.sh 在目标机现场下载的逻辑保留为兜底（文件已就位则自动跳过）。
var mysqlConnectorJar = model.StackAssetNeed{
	Key:      "mysql-connector-j",
	Version:  "8.4.0",
	FileName: "mysql-connector-j-8.4.0.jar",
	// 仅承载 Hive 实例的机器真正挂载使用；分发到全部成员机由 ha.sh 的存在性判断兜底过滤
	RemoteDir: "{{home_dir}}/hive/lib",
	Desc:      "Hive HA · MySQL 元数据库驱动（部署时自动分发到各主机）",
	// 与 ha.sh 内建下载同源：阿里云镜像优先，Maven Central 兜底
	FetchURLs: []string{
		"https://maven.aliyun.com/repository/public/com/mysql/mysql-connector-j/8.4.0/mysql-connector-j-8.4.0.jar",
		"https://repo1.maven.org/maven2/com/mysql/mysql-connector-j/8.4.0/mysql-connector-j-8.4.0.jar",
	},
}

// RequiredAssets 声明当前部署需要的离线资产：
// 仅 HA 模式且勾选 Hive 时需要 MySQL 驱动（单机模式 metastore 用 Derby，不需要）。
func (d *Driver) RequiredAssets(mode string, params map[string]string) []model.StackAssetNeed {
	if mode != "cluster" || !stackkit.IsYes(stackkit.TrimParam(params, "ha")) {
		return nil
	}
	hasHive := false
	for _, c := range strings.Split(stackkit.TrimParam(params, "components"), ",") {
		if strings.TrimSpace(c) == "hive" {
			hasHive = true
			break
		}
	}
	if !hasHive {
		return nil
	}
	return []model.StackAssetNeed{mysqlConnectorJar}
}
