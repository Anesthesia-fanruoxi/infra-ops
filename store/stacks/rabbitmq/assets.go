// 离线资产声明：实现 stackkit.AssetProvisioner（可选能力，引擎经 SFTP 分发）。
//
// 用户口径：优先使用本地（已上传/已登记的资产由引擎直接分发到各主机），
// 本地不存在时再由目标机现场从远程下载（node.sh 的 GitHub 下载逻辑保留为兜底）。
package rabbitmq

import (
	"regexp"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// rbSeriesRE 从镜像标签提取主系列（x.y），与 scripts/node.sh 的 grep -oE '^[0-9]+\.[0-9]+' 同源。
var rbSeriesRE = regexp.MustCompile(`^[0-9]+\.[0-9]+`)

// rbPluginTags 插件版本矩阵：系列 → {文件版本, release 标签}。
// 取自官方 release 归档终态（3.10 / 3.11 标签无 v 前缀）；与 scripts/node.sh 的 case 表严格同源——
// 改一处必须同步另一处，否则分发文件名与脚本推导不一致会触发重复下载。
var rbPluginTags = map[string][2]string{
	"4.2":  {"4.2.0", "v4.2.0"},
	"4.1":  {"4.1.0", "v4.1.0"},
	"4.0":  {"4.0.7", "v4.0.7"},
	"3.13": {"3.13.0", "v3.13.0"},
	"3.12": {"3.12.0", "v3.12.0"},
	"3.11": {"3.11.1", "3.11.1"},
	"3.10": {"3.10.2", "3.10.2"},
}

const (
	rbPluginBase    = "rabbitmq_delayed_message_exchange"
	rbPluginRepoURL = "https://github.com/rabbitmq/rabbitmq-delayed-message-exchange/releases/download"
)

// rbPluginNeed 延迟队列插件 .ez 的资产需求（按镜像标签推导系列；无法解析返回 ok=false）。
func rbPluginNeed(image string) (model.StackAssetNeed, bool) {
	tag := image
	if i := strings.LastIndex(tag, ":"); i >= 0 {
		tag = tag[i+1:]
	}
	series := rbSeriesRE.FindString(tag)
	if series == "" {
		return model.StackAssetNeed{}, false
	}
	// 未知系列走 `${series}.0`（与 node.sh 的 * 分支一致）
	ver, ptag := series+".0", "v"+series+".0"
	if v, ok := rbPluginTags[series]; ok {
		ver, ptag = v[0], v[1]
	}
	file := rbPluginBase + "-" + ver + ".ez"
	raw := rbPluginRepoURL + "/" + ptag + "/" + file
	return model.StackAssetNeed{
		Key:       rbPluginBase,
		Version:   ver,
		FileName:  file,
		RemoteDir: "{{home_dir}}/plugins",
		Desc:      "RabbitMQ 延迟队列插件（部署时自动分发到各主机的 plugins 目录）",
		// 服务端代下按序尝试：国内可达的加速代理优先，GitHub 直连垫底。
		// 实测（2026-09-16 本机）raw github.com 直连超时，三个代理 0.6~2.2s 可达——
		// 把 raw 排首位会让代下先白等一个连接超时、甚至重试耗尽而整体失败。
		// 顺序与 scripts/node.sh 的 PROXIES 轮换同族：改一处必须同步另一处。
		FetchURLs: []string{
			"https://gh-proxy.com/" + raw,
			"https://gh.ddlc.top/" + raw,
			"https://ghfast.top/" + raw,
			raw,
		},
	}, true
}

// RequiredAssets 声明当前部署需要的离线资产：仅集群模式且 delayed_plugin 开启时，
// 需要延迟队列插件 .ez（版本按 image 标签推导；无法推导则不声明，由 node.sh 现场下载兜底）。
func (d *Driver) RequiredAssets(mode string, params map[string]string) []model.StackAssetNeed {
	if mode != "cluster" || !stackkit.IsYes(stackkit.TrimParam(params, "delayed_plugin")) {
		return nil
	}
	image := stackkit.TrimParam(params, "image")
	if image == "" {
		image = "rabbitmq:3.13-management"
	}
	if need, ok := rbPluginNeed(image); ok {
		return []model.StackAssetNeed{need}
	}
	return nil
}
