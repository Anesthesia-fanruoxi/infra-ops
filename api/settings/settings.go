// Package settings 平台设置：AI 接入与系统信息（只读）。
//
// 设置页是平台级配置的唯一入口——AI 配置原先挂在 MySQL 工具下（/api/mysql/ai/config），
// 现在收归这里，任何功能要调模型都从 settings 表取同一套配置（见 common/aiconfig）。
package settings

import (
	"strings"
	"time"

	"infra-ops/common/crypto"
	"infra-ops/store/setting"
)

// startedAt 进程启动时刻，用于「系统信息」里的运行时长。
var startedAt = time.Now()

// Deps 处理器依赖（装配处一次性注入）。
type Deps struct {
	Settings *setting.SettingsRepo
	CryptoS  *crypto.Service
}

// Handler 设置接口处理器。
type Handler struct {
	settings *setting.SettingsRepo
	cryptoS  *crypto.Service
}

// NewHandler 创建设置处理器。
func NewHandler(d Deps) *Handler {
	return &Handler{settings: d.Settings, cryptoS: d.CryptoS}
}

// get 读取单个配置项（去空白）；缺失返回空串。
func (h *Handler) get(key string) string {
	if h.settings == nil {
		return ""
	}
	v, _ := h.settings.Get(key)
	return strings.TrimSpace(v)
}
