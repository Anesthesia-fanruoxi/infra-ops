// ai.go AI 接入配置：读取、保存与连通性测试。
package settings

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiconfig"
	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/setting"
)

const (
	// aiTestPrompt 连通性测试的提示词：要求极短回复，把 token 消耗压到最低。
	aiTestPrompt = "请只回复两个字：可用"
	// aiTestReplyMax 回显的模型回复截断长度。
	aiTestReplyMax = 200
)

// GetAI GET /api/settings/ai
func (h *Handler) GetAI(c *gin.Context) {
	resp.OK(c, h.aiView())
}

func (h *Handler) aiView() model.AIConfig {
	s := aiconfig.Load(h.settings, h.cryptoS)
	presets := aiconfig.Providers()
	providers := make([]model.AIProvider, 0, len(presets))
	for _, p := range presets {
		providers = append(providers, model.AIProvider{ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, KeyHint: p.KeyHint})
	}
	return model.AIConfig{
		Enabled:   s.Enabled,
		Provider:  s.Provider,
		Providers: providers,
		BaseURL:   s.BaseURL,
		Model:     s.Model,
		HasKey:    aiconfig.MaskKey(s),
		Timeout:   s.TimeoutSeconds(),
	}
}

type aiSaveReq struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"` // 内置接入点 ID（见 aiconfig.Providers）；为空则用自定义地址
	BaseURL  string `json:"base_url"` // 仅自定义厂商时生效；内置厂商由平台派生
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`   // 留空 = 不修改已存密钥
	ClearKey bool   `json:"clear_key"` // 显式清空密钥
	Timeout  int    `json:"timeout"`
}

// SaveAI PUT /api/settings/ai
//
// 只有「启用」时才校验地址与模型必填——先存下来稍后再填也是合理用法，
// 不该因为开关是关的就拒绝保存半成品配置。
// 选了内置厂商就不必（也不该）填地址：地址由厂商预设派生，用户只填密钥即可。
func (h *Handler) SaveAI(c *gin.Context) {
	var req aiSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	provider := strings.TrimSpace(req.Provider)
	baseURL := resolveBaseURL(provider, req.BaseURL)
	modelName := strings.TrimSpace(req.Model)
	if req.Enabled {
		if baseURL == "" {
			resp.Fail(c, resp.CodeBadRequest, "启用前请先选择厂商")
			return
		}
		if modelName == "" {
			resp.Fail(c, resp.CodeBadRequest, "启用前请先选择模型")
			return
		}
	}
	timeout := int(aiconfig.ClampTimeout(req.Timeout) / time.Second)

	sets := [][2]string{
		{setting.SettingAIProvider, provider},
		{setting.SettingAIEnabled, boolFlag(req.Enabled)},
		{setting.SettingAIBaseURL, baseURL},
		{setting.SettingAIModel, modelName},
		{setting.SettingAITimeout, strconv.Itoa(timeout)},
	}
	if req.ClearKey {
		sets = append(sets, [2]string{setting.SettingAIAPIKey, ""})
	} else if key := strings.TrimSpace(req.APIKey); key != "" {
		enc, err := aiconfig.EncryptKey(h.cryptoS, key)
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "密钥加密失败")
			return
		}
		sets = append(sets, [2]string{setting.SettingAIAPIKey, enc})
	}
	for _, kv := range sets {
		if err := h.settings.Set(kv[0], kv[1]); err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "保存 AI 配置失败")
			return
		}
	}
	resp.OK(c, h.aiView())
}

// aiTestReq 连通性测试入参。用的是**表单当前值**而非库内值，故允许字段留空，
// 留空项回落库内已存值——这样「只改了一个地址想先试试」也能直接测。
type aiTestReq struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	Timeout  int    `json:"timeout"`
}

// TestAI POST /api/settings/ai/test
//
// 保存前先验一次：base_url 后缀、模型名、密钥这三样正是最容易配错的地方，
// 真出了问题再去翻「生成为什么失败」的成本远高于点一下测试。
// 失败以 200 + ok=false 返回（属于业务结果，不是请求错误）。
func (h *Handler) TestAI(c *gin.Context) {
	var req aiTestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	stored := aiconfig.Load(h.settings, h.cryptoS)

	provider := pick(req.Provider, stored.Provider)
	baseURL := resolveBaseURL(provider, pick(req.BaseURL, stored.BaseURL))
	modelName := pick(req.Model, stored.Model)
	key := pick(req.APIKey, stored.APIKey) // 未重输密钥时沿用已存的
	if baseURL == "" || modelName == "" {
		resp.OK(c, model.AITestResult{OK: false, Error: "请先选择厂商与模型"})
		return
	}

	timeout := aiconfig.ClampTimeout(req.Timeout)
	client := aiopenai.New(aiopenai.Config{
		BaseURL: baseURL, APIKey: key, Model: modelName, Timeout: timeout,
	})
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	started := time.Now()
	res, err := client.Chat(ctx, []aiopenai.Message{{Role: "user", Content: aiTestPrompt}})
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		resp.OK(c, model.AITestResult{OK: false, Error: err.Error(), LatencyMs: elapsed})
		return
	}
	out := model.AITestResult{
		OK:          true,
		Model:       res.Model,
		Reply:       truncateRunes(res.Content, aiTestReplyMax),
		LatencyMs:   elapsed,
		TotalTokens: res.Usage.TotalTokens,
		TokenSource: model.TokenSourceEstimated,
	}
	if res.Usage.FromAPI {
		out.TokenSource = model.TokenSourceAPI
	}
	resp.OK(c, out)
}

// aiModelsReq 拉取模型清单入参。与测试同理，用表单当前值，未填项回落库内已存值，
// 这样「刚填完密钥还没保存」也能先把模型拉出来选。
type aiModelsReq struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Timeout  int    `json:"timeout"`
}

// ListAIModels POST /api/settings/ai/models
//
// 选好厂商、填好密钥后自动拉取厂商实际提供的模型清单，让用户从下拉里挑，
// 省得手填模型名拼错。失败以 200 + ok=false 返回（业务结果）。
func (h *Handler) ListAIModels(c *gin.Context) {
	var req aiModelsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	stored := aiconfig.Load(h.settings, h.cryptoS)

	provider := pick(req.Provider, stored.Provider)
	baseURL := resolveBaseURL(provider, pick(req.BaseURL, stored.BaseURL))
	key := pick(req.APIKey, stored.APIKey)
	if baseURL == "" {
		resp.OK(c, model.AIModelList{OK: false, Error: "请先选择厂商"})
		return
	}

	timeout := aiconfig.ClampTimeout(req.Timeout)
	client := aiopenai.New(aiopenai.Config{BaseURL: baseURL, APIKey: key, Timeout: timeout})
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		resp.OK(c, model.AIModelList{OK: false, Error: err.Error()})
		return
	}
	resp.OK(c, model.AIModelList{OK: true, Models: models})
}

// resolveBaseURL 解析生效的接口地址：内置厂商用预设地址（用户不必手填），
// 厂商为空或未知时用传入的自定义地址。
func resolveBaseURL(provider, custom string) string {
	if p, ok := aiconfig.ProviderByID(provider); ok {
		return p.BaseURL
	}
	return strings.TrimSpace(custom)
}

// pick 取表单值，为空则回落库内已存值。
func pick(form, stored string) string {
	if v := strings.TrimSpace(form); v != "" {
		return v
	}
	return strings.TrimSpace(stored)
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// truncateRunes 按字符（而非字节）截断，避免切坏中文。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
