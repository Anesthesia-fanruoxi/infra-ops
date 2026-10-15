// grok_ai_trace.go Grok 生成的 AI 调用记录装配。
//
// 与 MySQL / 监控两侧共用同一张 ai_logs 表（menu=es / kind=grok），审计页按菜单与类型汇总；
// 生成侧可能重试，一轮多次调用按累加记（fill 累加、done 幂等落库），与 metrics 侧同一口径。
package es

import (
	"time"

	"infra-ops/common/aiopenai"
	"infra-ops/model"
)

// ---------------- 调用记录 ----------------

// grokTrace 一次 Grok 生成的记录装配（重试会累加用量，done 幂等落库）。
type grokTrace struct {
	h      *Handler
	row    model.AILog
	start  time.Time
	calls  int
	status string
	reason string
	saved  bool
}

func (h *Handler) beginGrokTrace(conn *model.ESConn, modelName, baseURL, hint string) *grokTrace {
	row := model.AILog{
		Menu: model.AIMenuES, Kind: model.AILogKindGrok,
		Model: modelName, BaseURL: baseURL,
		Question: orNone(hint), Status: model.LogStatusOK,
	}
	if conn != nil {
		row.ConnID = conn.ID
		row.ConnName = conn.Name
	}
	return &grokTrace{h: h, start: time.Now(), row: row, status: model.LogStatusOK}
}

func (t *grokTrace) fill(res *aiopenai.Result) {
	if res == nil {
		return
	}
	t.calls++
	if res.Model != "" {
		t.row.Model = res.Model
	}
	t.row.Content = truncateRunes(res.Content, 4000)
	t.row.PromptTokens += res.Usage.PromptTokens
	t.row.CompletionTokens += res.Usage.CompletionTokens
	t.row.TotalTokens += res.Usage.TotalTokens
	if !res.Usage.FromAPI {
		t.row.TokenSource = model.TokenSourceEstimated
	} else if t.row.TokenSource == "" {
		t.row.TokenSource = model.TokenSourceAPI
	}
}

func (t *grokTrace) fail(err error) {
	if err == nil {
		return
	}
	t.status = model.LogStatusError
	if t.reason == "" {
		t.reason = err.Error()
	}
}

func (t *grokTrace) done() {
	if t.saved {
		return
	}
	t.saved = true
	if t.h.aiLogRepo == nil {
		return
	}
	t.row.ElapsedMs = time.Since(t.start).Milliseconds()
	t.row.Status = t.status
	t.row.Error = truncateRunes(t.reason, 600)
	_, _ = t.h.aiLogRepo.Add(&t.row)
}

func (t *grokTrace) usage() model.MetricsAIUsage {
	return model.MetricsAIUsage{
		Calls:            t.calls,
		PromptTokens:     int64(t.row.PromptTokens),
		CompletionTokens: int64(t.row.CompletionTokens),
		TotalTokens:      int64(t.row.TotalTokens),
		Source:           t.row.TokenSource,
		ElapsedMs:        time.Since(t.start).Milliseconds(),
	}
}
