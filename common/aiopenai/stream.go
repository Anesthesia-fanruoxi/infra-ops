// stream.go Chat Completions 的流式补全：请求带 stream:true，响应按 SSE 逐帧解析。
//
// 与 Chat 共用端点归一化与用量口径：聚合后的内容、token 估算兜底完全一致，
// 调用方只多一个增量回调。用量随流返回依赖 stream_options.include_usage，
// 个别网关不认识该参数会整单拒绝——它是锦上添花，遭拒时去掉重试一次即可。
package aiopenai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxStreamBytes 流式响应体上限：正常补全远小于此，设上限避免异常端点把内存吃满。
const maxStreamBytes = 16 << 20

// DeltaFunc 增量文本回调；返回 error 即中止并关闭流（客户端断开等场景）。
type DeltaFunc func(text string) error

// streamChunk 一个 SSE 数据帧里的补全增量（OpenAI 兼容）。
type streamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatStream 流式对话补全：增量经 onDelta 回调，结束后返回聚合结果（口径同 Chat）。
//
// 超时语义与 Chat 不同（关键修正）：cfg.Timeout 在流式下是**空闲超时**——多久没有收到
// 新数据才算卡死；生成整体耗时不受限。原因是 http.Client.Timeout 属于「整体超时」，
// 连读 body 都算在内，用它兜流式会把正常的慢生成掐死在读流阶段
// （实测报错：context deadline exceeded ... Client.Timeout while reading body）。
func (c *Client) ChatStream(ctx context.Context, msgs []Message, onDelta DeltaFunc) (*Result, error) {
	endpoint := c.Endpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("未配置 AI 接口地址")
	}
	if strings.TrimSpace(c.cfg.Model) == "" {
		return nil, fmt.Errorf("未配置模型名称")
	}

	// 空闲看门狗：每读到一行数据就续期，只有连续 cfg.Timeout 无数据时才 cancel 掐断
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := time.AfterFunc(c.cfg.Timeout, cancel)
	defer idle.Stop()

	resp, err := c.startStream(streamCtx, endpoint, msgs, true)
	if err != nil {
		return nil, c.streamErr("请求 AI 服务失败", streamCtx, ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		resp.Body.Close()
		if !streamOptionsRejected(resp.StatusCode, raw) {
			return nil, streamStatusError(resp.StatusCode, raw)
		}
		// 端点不认 stream_options：去掉重试一次；此时流还没开始读，不会重复回调增量
		resp, err = c.startStream(streamCtx, endpoint, msgs, false)
		if err != nil {
			return nil, c.streamErr("请求 AI 服务失败", streamCtx, ctx, err)
		}
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
			resp.Body.Close()
			return nil, streamStatusError(resp.StatusCode, raw)
		}
	}
	defer resp.Body.Close()

	br := bufio.NewReader(io.LimitReader(resp.Body, maxStreamBytes))
	var content strings.Builder
	var usage *chatUsage
	model := ""
	for {
		line, readErr := br.ReadString('\n')
		if readErr == nil {
			idle.Reset(c.cfg.Timeout) // 数据在持续到达：看门狗续期
		}
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			// 只处理 data: 帧；event: / id: / retry: / ": 注释" / 空行均忽略
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				data = strings.TrimSpace(data)
				switch {
				case data == "[DONE]":
					return finishStream(content.String(), model, usage, msgs)
				case data != "":
					// 单帧解析失败不断流：个别网关会混入非规范帧，跳过即可
					var ch streamChunk
					if json.Unmarshal([]byte(data), &ch) == nil {
						if ch.Error != nil && ch.Error.Message != "" {
							return nil, fmt.Errorf("AI 服务报错：%s", ch.Error.Message)
						}
						if model == "" {
							model = strings.TrimSpace(ch.Model)
						}
						if ch.Usage != nil {
							usage = ch.Usage
						}
						for _, choice := range ch.Choices {
							if choice.Delta.Content == "" {
								continue
							}
							content.WriteString(choice.Delta.Content)
							if onDelta != nil {
								if err := onDelta(choice.Delta.Content); err != nil {
									return nil, err
								}
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				// 端点没发 [DONE] 直接断流：按正常结束处理（以收到的内容为准）
				return finishStream(content.String(), model, usage, msgs)
			}
			return nil, c.streamErr("读取 AI 流式响应失败", streamCtx, ctx, readErr)
		}
	}
}

// streamErr 包装流式阶段的错误：空闲看门狗掐断时给「超时」语义的提示与建议，
// 而不是把内部的 context canceled 原样甩给用户；父 ctx 取消（用户关面板 / 断连）
// 仍按原始错误返回。
func (c *Client) streamErr(stage string, streamCtx, parentCtx context.Context, err error) error {
	if streamCtx.Err() != nil && parentCtx.Err() == nil {
		return fmt.Errorf("AI 流式响应超时：%v 未收到新数据（可在「设置」里调大 AI 超时）", c.cfg.Timeout)
	}
	return fmt.Errorf("%s: %w", stage, err)
}

// startStream 发起流式请求；includeUsage 控制是否携带 stream_options。
func (c *Client) startStream(ctx context.Context, endpoint string, msgs []Message, includeUsage bool) (*http.Response, error) {
	payload := map[string]interface{}{
		"model":       strings.TrimSpace(c.cfg.Model),
		"messages":    msgs,
		"temperature": 0.2,
		"stream":      true,
	}
	if includeUsage {
		payload["stream_options"] = map[string]bool{"include_usage": true}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return c.shc.Do(req)
}

// finishStream 聚合收尾：内容、模型名与用量口径与 Chat 完全一致。
func finishStream(raw string, model string, usage *chatUsage, msgs []Message) (*Result, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, fmt.Errorf("AI 服务返回了空内容")
	}
	res := &Result{Content: text, Model: model}
	res.Usage = normalizeUsage(usage, msgs, text)
	return res, nil
}

// streamOptionsRejected 判断整单失败是否因端点不认识 stream_options。
// 只在「参数类错误 + 报错文本点名该参数」时重试：不认识参数却不说参数名的端点
// 无法识别，宁可原样报错，也不盲目重发。
func streamOptionsRejected(status int, raw []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return false
	}
	return bytes.Contains(bytes.ToLower(raw), []byte("stream_options"))
}

// streamStatusError 从错误响应体提取错误信息，口径与 Chat 一致。
func streamStatusError(status int, raw []byte) error {
	var out chatResponse
	_ = json.Unmarshal(raw, &out)
	if out.Error != nil && out.Error.Message != "" {
		return fmt.Errorf("AI 服务返回 %d：%s", status, out.Error.Message)
	}
	return fmt.Errorf("AI 服务返回 %d：%s", status, truncate(string(raw), 300))
}
