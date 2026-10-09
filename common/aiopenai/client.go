// Package aiopenai 极简的 OpenAI 兼容 Chat Completions 客户端。
//
// 只依赖 net/http，不引入任何厂商 SDK：DeepSeek、通义、Kimi、OpenAI、
// 本地 vLLM / Ollama 的 /v1 接口形状一致，因此一套代码通吃。
package aiopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout = 60 * time.Second
	// 响应体上限：正常补全远小于此，设上限避免异常响应把内存吃满
	maxRespBytes = 8 << 20
)

// Config 接入配置。BaseURL 只给主机名时会补 /v1；已带路径（/v1、/v1beta/openai、
// /api/paas/v4 之类）则原样使用，因为各厂商的 API 根路径并不都是 /v1。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// Message 一条对话消息。
type Message struct {
	Role    string `json:"role"` // system / user / assistant
	Content string `json:"content"`
}

// Usage 一次补全的 token 用量。
//
// FromAPI 为 false 表示接口没回 usage（不少网关与本地推理服务都不回），
// 此时各字段是本地按字符估算的近似值，调用方**必须**把来源一并展示出去，
// 否则会被当成接口给的精确值。
type Usage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	FromAPI          bool `json:"from_api"`
}

// Result 一次补全的结果。
type Result struct {
	Content string // 首条候选的文本内容（已 TrimSpace）
	Model   string // 服务端回显的模型名，部分端点会返回实际路由到的模型
	Usage   Usage
}

// Client Chat Completions 客户端。
type Client struct {
	cfg Config
	hc  *http.Client
}

// New 创建客户端；Timeout 未给定时取默认值。
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Client{cfg: cfg, hc: &http.Client{Timeout: cfg.Timeout}}
}

// base 归一化出 API 根地址：去尾斜杠、剥离已带的具体端点。
//
// 只有「只填了主机名」时才补 /v1——各厂商的 API 根路径并不统一：
// Gemini 是 /v1beta/openai、智谱是 /api/paas/v4、火山方舟是 /api/v3、千帆是 /v2，
// 一旦带了路径就说明用户/预设知道正确的根在哪，再拼 /v1 只会得到不存在的地址。
func (c *Client) base() string {
	base := strings.TrimRight(strings.TrimSpace(c.cfg.BaseURL), "/")
	if base == "" {
		return ""
	}
	base = strings.TrimSuffix(base, "/chat/completions")
	base = strings.TrimSuffix(base, "/models")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base // 非标准地址（如本地 socket 简写）：原样交给调用方
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1"
		return strings.TrimRight(u.String(), "/")
	}
	return base
}

// Endpoint 归一化出 chat/completions 的完整地址：
//
//	https://api.deepseek.com    -> https://api.deepseek.com/v1/chat/completions
//	https://api.deepseek.com/v1 -> https://api.deepseek.com/v1/chat/completions
//	http://127.0.0.1:11434/v1   -> http://127.0.0.1:11434/v1/chat/completions
//	https://open.bigmodel.cn/api/paas/v4 -> （路径原样保留）/chat/completions
func (c *Client) Endpoint() string {
	if b := c.base(); b != "" {
		return b + "/chat/completions"
	}
	return ""
}

// ModelsEndpoint 归一化出 /v1/models 的完整地址，用于拉取可用模型清单。
func (c *Client) ModelsEndpoint() string {
	if b := c.base(); b != "" {
		return b + "/models"
	}
	return ""
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat 发起一次对话补全，返回首条候选的内容与 token 用量。
func (c *Client) Chat(ctx context.Context, msgs []Message) (*Result, error) {
	endpoint := c.Endpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("未配置 AI 接口地址")
	}
	if strings.TrimSpace(c.cfg.Model) == "" {
		return nil, fmt.Errorf("未配置模型名称")
	}

	payload, err := json.Marshal(map[string]interface{}{
		"model":       strings.TrimSpace(c.cfg.Model),
		"messages":    msgs,
		"temperature": 0.2,
		"stream":      false,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 AI 服务失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, fmt.Errorf("读取 AI 响应失败: %w", err)
	}
	var out chatResponse
	_ = json.Unmarshal(raw, &out)

	if resp.StatusCode != http.StatusOK {
		if out.Error != nil && out.Error.Message != "" {
			return nil, fmt.Errorf("AI 服务返回 %d：%s", resp.StatusCode, out.Error.Message)
		}
		return nil, fmt.Errorf("AI 服务返回 %d：%s", resp.StatusCode, truncate(string(raw), 300))
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("AI 服务报错：%s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("AI 服务未返回任何候选结果")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("AI 服务返回了空内容")
	}

	res := &Result{Content: content, Model: strings.TrimSpace(out.Model)}
	res.Usage = normalizeUsage(out.Usage, msgs, content)
	return res, nil
}

type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ListModels 拉取 /v1/models 的可用模型 ID 列表（OpenAI 兼容端点通用）。
// 让用户从厂商实际提供的模型里挑，省得手填模型名拼错——这是配置里第二容易出错的地方。
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	endpoint := c.ModelsEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("未配置 AI 接口地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, fmt.Errorf("读取模型列表失败: %w", err)
	}
	var out modelListResponse
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil && out.Error.Message != "" {
			return nil, fmt.Errorf("模型列表接口返回 %d：%s", resp.StatusCode, out.Error.Message)
		}
		return nil, fmt.Errorf("模型列表接口返回 %d：%s", resp.StatusCode, truncate(string(raw), 300))
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("模型列表接口报错：%s", out.Error.Message)
	}

	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("模型列表接口未返回任何模型")
	}
	return models, nil
}

// normalizeUsage 取接口返回的 usage；接口没给时退回本地估算（并标出来源）。
func normalizeUsage(u *chatUsage, msgs []Message, content string) Usage {
	if u != nil && (u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0) {
		out := Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, FromAPI: true}
		if out.TotalTokens == 0 {
			out.TotalTokens = out.PromptTokens + out.CompletionTokens
		}
		return out
	}
	prompt := EstimateTokens(joinMessages(msgs))
	completion := EstimateTokens(content)
	return Usage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: prompt + completion}
}

// joinMessages 把消息拼成一段文本，仅用于估算输入侧 token。
func joinMessages(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteByte('\n')
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// EstimateTokens 估算一段文本的 token 数。
//
// 只在接口没返回 usage 时兜底：中日韩文字按每字约 1 token，其余字符按每 4 个
// 约 1 token（与主流 BPE 词表的量级一致）。它是近似值，不是精确计数。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	wide, narrow := 0, 0
	for _, r := range s {
		switch {
		case r >= 0x2E80 && r <= 0x9FFF, // CJK 部首扩展 ~ 统一表意文字
			r >= 0xAC00 && r <= 0xD7A3, // 谚文音节
			r >= 0xF900 && r <= 0xFAFF, // CJK 兼容表意文字
			r >= 0xFF00 && r <= 0xFF60, // 全角字符
			r >= 0x3000 && r <= 0x303F: // CJK 标点
			wide++
		default:
			narrow++
		}
	}
	n := wide + (narrow+3)/4
	if n < 1 {
		n = 1
	}
	return n
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
