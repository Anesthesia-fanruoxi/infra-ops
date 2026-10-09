// Package aiconfig 平台级 AI 接入配置：从 settings 表读取、归一化并构造客户端。
//
// 放在公共层的原因：AI 不再是某个工具的功能，而是平台能力——MySQL 的自然语言生成 SQL、
// 语义目录归纳，以及后续新增的功能都从这里取配置与客户端，避免「同一套配置多处解析」
// 导致密钥解码方式、超时钳制、启用判断这些细节各写一遍、各不一致。
package aiconfig

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"infra-ops/common/aiopenai"
	"infra-ops/common/crypto"
	"infra-ops/store/setting"
)

const (
	// DefaultTimeoutSeconds 未配置超时时的默认值（秒）。
	DefaultTimeoutSeconds = 90
	// MaxTimeoutSeconds 超时上限（秒）：长上下文批量归纳需要余量，但不允许无限等待。
	MaxTimeoutSeconds = 600
	// MinTimeoutSeconds 超时下限（秒）。
	MinTimeoutSeconds = 10
)

// Provider 一个内置接入点预设：选定后接口地址由平台派生，用户只需填密钥。
// 同一厂商可能有多个地址（如小米 MiMo 的按量付费与 Token Plan 分属不同集群），
// 故这里按「接入点」而非「厂商」粒度登记，让用户直接选到正确的地址。
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	KeyHint string `json:"key_hint"` // 密钥格式提示（各接入点前缀不同，如 sk- / tp-）
}

// providers 内置接入点清单。均提供 OpenAI 兼容接口（地址根路径各不相同，
// 由 aiopenai 原样保留），故共用一套客户端。
//
// 顺序即下拉顺序：国际主流 → 国内主流 → 小米 MiMo。同一厂商的地区/计费集群
// 各登记一条，让用户直接选到正确的地址。
var providers = []Provider{
	// ---- 国际主流 ----
	{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", KeyHint: "sk-xxxx"},
	{ID: "anthropic", Name: "Anthropic Claude", BaseURL: "https://api.anthropic.com/v1", KeyHint: "sk-ant-xxxx"},
	{ID: "google-gemini", Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", KeyHint: "AIza…"},
	{ID: "xai", Name: "xAI Grok", BaseURL: "https://api.x.ai/v1", KeyHint: "xai-xxxx"},
	{ID: "mistral", Name: "Mistral", BaseURL: "https://api.mistral.ai/v1", KeyHint: "xxxx"},
	{ID: "groq", Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", KeyHint: "gsk_xxxx"},
	{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", KeyHint: "sk-or-xxxx"},
	{ID: "together", Name: "Together AI", BaseURL: "https://api.together.xyz/v1", KeyHint: "xxxx"},
	{ID: "nvidia", Name: "NVIDIA NIM", BaseURL: "https://integrate.api.nvidia.com/v1", KeyHint: "nvapi-xxxx"},
	{ID: "cohere", Name: "Cohere", BaseURL: "https://api.cohere.ai/compatibility/v1", KeyHint: "xxxx"},
	{ID: "moonshotai", Name: "Moonshot Kimi（国际）", BaseURL: "https://api.moonshot.ai/v1", KeyHint: "sk-xxxx"},
	{ID: "zai", Name: "Z.ai GLM（国际）", BaseURL: "https://api.z.ai/api/paas/v4", KeyHint: "xxxx.xxxx"},
	{ID: "agnes", Name: "Agnes AI", BaseURL: "https://apihub.agnes-ai.com/v1", KeyHint: "sk-xxxx"},
	// ---- 国内主流 ----
	{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", KeyHint: "sk-xxxx"},
	{ID: "dashscope", Name: "阿里通义千问", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", KeyHint: "sk-xxxx"},
	{ID: "moonshot-cn", Name: "月之暗面 Kimi（中国）", BaseURL: "https://api.moonshot.cn/v1", KeyHint: "sk-xxxx"},
	{ID: "zhipu", Name: "智谱 GLM", BaseURL: "https://open.bigmodel.cn/api/paas/v4", KeyHint: "xxxx.xxxx"},
	{ID: "doubao", Name: "字节豆包（火山方舟）", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", KeyHint: "xxxx"},
	{ID: "hunyuan", Name: "腾讯混元", BaseURL: "https://api.hunyuan.cloud.tencent.com/v1", KeyHint: "sk-xxxx"},
	{ID: "qianfan", Name: "百度文心（千帆）", BaseURL: "https://qianfan.baidubce.com/v2", KeyHint: "bce-v3/xxxx"},
	{ID: "spark", Name: "讯飞星火", BaseURL: "https://spark-api-open.xf-yun.com/v1", KeyHint: "xxxx"},
	{ID: "minimax", Name: "MiniMax", BaseURL: "https://api.minimaxi.com/v1", KeyHint: "xxxx"},
	{ID: "stepfun", Name: "阶跃星辰", BaseURL: "https://api.stepfun.com/v1", KeyHint: "sk-xxxx"},
	{ID: "siliconflow", Name: "硅基流动", BaseURL: "https://api.siliconflow.cn/v1", KeyHint: "sk-xxxx"},
	{ID: "lingyiwanwu", Name: "零一万物", BaseURL: "https://api.lingyiwanwu.com/v1", KeyHint: "xxxx"},
	// ---- 小米 MiMo ----
	// 按量付费：单地址，密钥 sk- 开头
	{ID: "mimo", Name: "小米 MiMo · 按量付费", BaseURL: "https://api.xiaomimimo.com/v1", KeyHint: "sk-xxxxx"},
	// Token Plan（订阅制）：按集群分地址，密钥 tp- 开头，与按量付费互不通用
	{ID: "mimo-token-cn", Name: "小米 MiMo · Token Plan（中国）", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", KeyHint: "tp-xxxxx"},
	{ID: "mimo-token-sgp", Name: "小米 MiMo · Token Plan（新加坡）", BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1", KeyHint: "tp-xxxxx"},
	{ID: "mimo-token-ams", Name: "小米 MiMo · Token Plan（欧洲）", BaseURL: "https://token-plan-ams.xiaomimimo.com/v1", KeyHint: "tp-xxxxx"},
}

// Providers 返回内置接入点清单（副本，避免调用方改动到包级变量）。
func Providers() []Provider {
	out := make([]Provider, len(providers))
	copy(out, providers)
	return out
}

// ProviderByID 按 ID 查内置接入点；未命中返回 false（表示自定义地址）。
func ProviderByID(id string) (Provider, bool) {
	id = strings.TrimSpace(id)
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Settings 归一化后的 AI 接入配置。
// APIKey 是解密后的明文，仅进程内使用，绝不下发前端。
type Settings struct {
	Provider string
	Enabled  bool
	BaseURL  string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

// TimeoutSeconds 返回秒数形式，便于接口层下发。
func (s Settings) TimeoutSeconds() int { return int(s.Timeout / time.Second) }

// Load 从 settings 表读取配置。密钥存的是「密文的 base64」，需解两层。
//
// BaseURL 以接入点预设为准：选了内置接入点就用预设地址，用户不用（也不该）手填；
// 接入点为空或未知时才回落库里的自定义地址，兼容旧配置。
func Load(repo *setting.SettingsRepo, cryptoS *crypto.Service) Settings {
	get := func(k string) string {
		if repo == nil {
			return ""
		}
		v, _ := repo.Get(k)
		return strings.TrimSpace(v)
	}
	provider := get(setting.SettingAIProvider)
	baseURL := get(setting.SettingAIBaseURL)
	if p, ok := ProviderByID(provider); ok {
		baseURL = p.BaseURL
	}
	s := Settings{
		Provider: provider,
		Enabled:  get(setting.SettingAIEnabled) == "1",
		BaseURL:  baseURL,
		Model:    get(setting.SettingAIModel),
	}
	if enc := get(setting.SettingAIAPIKey); enc != "" && cryptoS != nil {
		if raw, err := base64.StdEncoding.DecodeString(enc); err == nil {
			if plain, err := cryptoS.Decrypt(raw); err == nil {
				s.APIKey = string(plain)
			}
		}
	}
	sec, err := strconv.Atoi(get(setting.SettingAITimeout))
	if err != nil || sec <= 0 {
		sec = DefaultTimeoutSeconds
	}
	s.Timeout = ClampTimeout(sec)
	return s
}

// ClampTimeout 把超时秒数钳制到 [MinTimeoutSeconds, MaxTimeoutSeconds]。
func ClampTimeout(sec int) time.Duration {
	if sec < MinTimeoutSeconds {
		sec = DefaultTimeoutSeconds
	}
	if sec > MaxTimeoutSeconds {
		sec = MaxTimeoutSeconds
	}
	return time.Duration(sec) * time.Second
}

// Client 校验配置齐备后返回可直接调用的客户端。
// 配置不齐时返回的 Settings 仍有效，便于调用方给出精确提示。
func Client(repo *setting.SettingsRepo, cryptoS *crypto.Service) (*aiopenai.Client, Settings, error) {
	s := Load(repo, cryptoS)
	if !s.Enabled {
		return nil, s, fmt.Errorf("AI 能力未启用，请先在「设置 · AI 接入」中开启")
	}
	if s.BaseURL == "" {
		return nil, s, fmt.Errorf("未配置 AI 接口地址")
	}
	if s.Model == "" {
		return nil, s, fmt.Errorf("未配置模型名称")
	}
	return aiopenai.New(aiopenai.Config{
		BaseURL: s.BaseURL,
		APIKey:  s.APIKey,
		Model:   s.Model,
		Timeout: s.Timeout,
	}), s, nil
}

// EncryptKey 加密密钥以落库：先做对称加密，再 base64 包裹（与 Load 的解码顺序对应）。
func EncryptKey(cryptoS *crypto.Service, plain string) (string, error) {
	if cryptoS == nil {
		return "", fmt.Errorf("加密服务不可用")
	}
	enc, err := cryptoS.Encrypt([]byte(plain))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// MaskKey 只回「是否已配置」，不泄露密钥内容。
func MaskKey(s Settings) bool { return s.APIKey != "" }
