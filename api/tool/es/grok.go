// grok.go ES 工具的 Grok 解析：贴一段样例日志 + grok pattern，逐行看解析出的字段。
//
// 为什么走本地引擎而不是集群的 ingest pipeline simulate：
//   - simulate 需要 manage_pipeline 权限，且要求集群在线；而「调 pattern」这件事
//     往往发生在集群还没建好、或手上只有一段日志的时候。
//   - 本地引擎（common/grok）纯函数、可离线、可对任意文本试跑，排错反馈更直接
//     （整行未命中时给出「部分命中」提示，而不是一个冷冰冰的空结果）。
//
// 代价是方言为 RE2，内置 pattern 库已按 RE2 改写（见 common/grok/patterns.go）。
package es

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/grok"
	"infra-ops/common/resp"
)

const (
	grokMaxLines     = 500    // 单次解析的行数上限
	grokDefaultLines = 200    // 未指定时的行数上限
	grokMaxLineRunes = 4096   // 单行截断长度（原始行回显也要控体积）
	grokMaxPatterns  = 20     // pattern 条数上限
	grokMaxTextRunes = 200000 // 样例文本总长上限
)

// grokParseReq 解析请求；pattern 与 patterns 二选一（前端用 patterns）。
type grokParseReq struct {
	Pattern     string            `json:"pattern"`
	Patterns    []string          `json:"patterns"`
	Text        string            `json:"text" binding:"required"`
	Definitions map[string]string `json:"definitions"`
	MaxLines    int               `json:"max_lines"`
}

// grokLineResult 单行解析结果。Partial 表示只有局部命中（字段可能只是片段）。
type grokLineResult struct {
	No         int          `json:"no"`
	Raw        string       `json:"raw"`
	Matched    bool         `json:"matched"`
	Partial    bool         `json:"partial"`
	PatternIdx int          `json:"pattern_index"`
	Fields     []grok.Field `json:"fields"`
}

// GrokParse 逐行解析样例日志（不访问集群，仅校验连接存在）。
func (h *Handler) GrokParse(c *gin.Context) {
	connID := shared.ParseID(c)
	if conn, err := h.repo.GetByID(connID); err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	var req grokParseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	patterns := normalizePatterns(req.Patterns, req.Pattern)
	if len(patterns) == 0 {
		resp.Fail(c, esCodeGrokInvalid, "请至少填写一条 grok pattern")
		return
	}
	if len(patterns) > grokMaxPatterns {
		resp.Fail(c, esCodeGrokInvalid, "pattern 条数最多 "+strconv.Itoa(grokMaxPatterns)+" 条")
		return
	}
	if n := len([]rune(req.Text)); n > grokMaxTextRunes {
		resp.Fail(c, esCodeGrokInvalid, "样例文本过长（"+strconv.Itoa(n)+" 字符，上限 "+strconv.Itoa(grokMaxTextRunes)+"）")
		return
	}
	matcher, err := grok.Cached(patterns, req.Definitions)
	if err != nil {
		// 编译失败是使用者最需要看清的一类错误：原样回传，前端在 pattern 框下提示
		resp.Fail(c, esCodeGrokInvalid, err.Error())
		return
	}

	limit := req.MaxLines
	if limit <= 0 {
		limit = grokDefaultLines
	}
	if limit > grokMaxLines {
		limit = grokMaxLines
	}
	rawLines := splitSampleLines(req.Text)
	truncated := len(rawLines) > limit
	if truncated {
		rawLines = rawLines[:limit]
	}

	lines := make([]grokLineResult, 0, len(rawLines))
	matched, partial, missed := 0, 0, 0
	for i, raw := range rawLines {
		item := grokLineResult{No: i + 1, Raw: truncateRunes(raw, grokMaxLineRunes), PatternIdx: -1}
		if m := matcher.Parse(raw); m != nil {
			item.PatternIdx = m.PatternIdx
			item.Fields = m.Fields
			item.Partial = m.Partial
			if m.Partial {
				partial++
			} else {
				matched++
			}
		} else {
			missed++
		}
		if item.Fields == nil {
			item.Fields = []grok.Field{}
		}
		lines = append(lines, item)
	}
	resp.OK(c, gin.H{
		"total":     len(lines),
		"matched":   matched,
		"partial":   partial,
		"missed":    missed,
		"truncated": truncated,
		"patterns":  patterns,
		"lines":     lines,
	})
}

// normalizePatterns 合并单条与多条写法，去空去重并保序。
func normalizePatterns(list []string, single string) []string {
	out := make([]string, 0, len(list)+1)
	seen := map[string]bool{}
	appendOne := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, p := range list {
		appendOne(p)
	}
	appendOne(single)
	return out
}

// splitSampleLines 按行切分样例，保留行内容（空行由调用方过滤后的行号即为原行号）。
func splitSampleLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// truncateRunes 按字符（非字节）截断，避免把多字节字符切坏。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
