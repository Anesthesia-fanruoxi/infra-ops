// ai.go MySQL 工具的 AI 能力入口：配置读取与客户端构造。
//
// 配置本身是**平台级**的（settings 表的 ai.* 键，由「设置」页维护），这里只做转发——
// 工具内需要模型时统一走 aiClient()，不自行解析 settings，也不再提供自己的配置读写接口。
package mysql

import (
	"infra-ops/common/aiconfig"
	"infra-ops/common/aiopenai"
)

// NL→SQL 时最多送进上下文的表数量（表多时按相关度挑选）。
const sqlContextTables = 20

// aiSettings 与公共包类型的别名：本包其余文件按字段直接访问，无需改动。
type aiSettings = aiconfig.Settings

// loadAISettings 读取平台级 AI 配置（密钥已解密，仅进程内使用）。
func (h *Handler) loadAISettings() aiSettings {
	return aiconfig.Load(h.settings, h.cryptoS)
}

// aiClient 校验配置齐备后返回客户端。
func (h *Handler) aiClient() (*aiopenai.Client, aiSettings, error) {
	return aiconfig.Client(h.settings, h.cryptoS)
}
