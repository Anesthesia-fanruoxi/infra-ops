// grok_logstash_ai.go Logstash 配置生成里的 AI 部分：只让模型判断 mutate 段该做什么。
//
// 边界（与 grok_logstash.go 的分工对应）：模型**不写配置文本**，只输出结构化的
// gsub / remove_field / notes，拼接与转义全在本地完成。这样即使模型给出的字段名有幻觉，
// 也只是「多一条被本地校验拦下的建议」，不会污染成语法错误的配置——而一份加载失败的
// logstash 配置排查起来远比少一条 gsub 麻烦。
//
// 非流式：配置文本很短，主体还是本地拼装，流式草稿在这里没有意义（不像 pattern 生成要边出边看）。
package es

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/aiconfig"
	"infra-ops/common/aiopenai"
	"infra-ops/common/grok"
	"infra-ops/common/resp"
	"infra-ops/model"
)

const grokMutatePromptHead = `你是日志采集配置专家。用户已经用 grok 把样例日志解析成字段，接下来需要一个 logstash mutate 段做「解析之后的收尾处理」。
你只负责判断该处理什么，配置文本由程序拼装，所以**不要输出任何 logstash 配置**，也不要输出解释文字或 markdown 代码块。

只输出这个 JSON：
{"gsub":[{"field":"字段名","from":"-","to":"0.0"}],"notes":"一句话"}

规则：
1. 你只管 gsub：用于「字段本身有值、但值是占位符（如 - 、空串）需要换成默认值」的情形；样例里没出现的字段不要造 gsub，缺字段和值为占位符是两件事。
2. gsub.field 必须是「字段清单」里的字段名，不许自造；from 按正则处理，要精确匹配整个值就写 ^...$，有正则元字符要转义。
3. **不要输出 remove_field**：删哪些字段（采集器元数据、原始整行）由程序按固定清单加界面开关决定，不归你判断，你也不用提。
4. 确实没有可处理的就把 gsub 留成空数组，不要硬凑；notes 用中文一句话（40 字以内）说明这版处理了什么，没有内容时留空字符串。`

const grokMutatePromptUser = `样例日志（共 %d 行）：
%s

grok 解析后产出的字段清单：
%s

日志来源 tag：%s
待解析字段：%s
补充要求：%s`

// enhanceState 增强结果的状态，前端据此着色（未启用不算失败，不该报成黄色告警）。
const (
	grokEnhanceOK      = "ok"
	grokEnhanceSkipped = "skipped" // AI 未启用/未配置：配置照常生成，只是没有 mutate 增强
	grokEnhanceFailed  = "failed"  // 调用或解析失败：配置照常生成
)

// grokMutateAdvice 取模型的 mutate 建议。返回 (建议, 状态, 失败原因)；
// 任何失败都不影响调用方继续输出配置——AI 只是加分项。
func (h *Handler) grokMutateAdvice(ctx context.Context, conn *model.ESConn, req grokLogstashReq, fields, samples []string) (*grokMutateAI, string, string) {
	// 「是否启用」由 handler 提前判过（它要在推 phase 帧之前就知道），这里只管取客户端；
	// 仍可能撞上「Enabled 为真但配置不完整」——按 skipped 处理：那是配置问题，不是这次生成失败。
	aiC, aiCfg, err := aiconfig.Client(h.settings, h.cryptoS)
	if err != nil {
		return nil, grokEnhanceSkipped, err.Error()
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "message"
	}
	trace := h.beginGrokTrace(conn, aiCfg.Model, aiCfg.BaseURL, "生成 logstash 配置："+strings.TrimSpace(req.Tag))
	defer trace.done()

	res, err := aiC.Chat(ctx, []aiopenai.Message{
		{Role: "system", Content: grokMutatePromptHead},
		{Role: "user", Content: fmt.Sprintf(grokMutatePromptUser,
			len(samples), strings.Join(samples, "\n"),
			strings.Join(limitRunes(fields, grokLogstashFieldMax), "、"),
			orNone(strings.TrimSpace(req.Tag)), source,
			orNone(strings.TrimSpace(req.Hint)))},
	})
	if err != nil {
		trace.fail(err)
		return nil, grokEnhanceFailed, err.Error()
	}
	trace.fill(res)
	// 配置生成不是 grok pattern 生成，记录里存的应当是「模型建议了什么」而不是原始 JSON 全文
	trace.row.Expr = truncateRunes(strings.Join(fields, ","), 2000)

	var reply grokMutateAI
	if err := unmarshalModelJSON(res.Content, &reply); err != nil {
		perr := fmt.Errorf("解析模型返回失败: %w", err)
		trace.fail(perr)
		return nil, grokEnhanceFailed, perr.Error()
	}
	return &reply, grokEnhanceOK, ""
}

// limitRunes 截断列表（提示词里的字段清单要控体积）。
func limitRunes(list []string, max int) []string {
	if len(list) <= max {
		return list
	}
	return list[:max]
}

// GrokLogstash POST /api/es/:id/grok/logstash
//
// 生成一份可直接粘贴的 Logstash filter：外层 if 条件按 tag、grok 块按当前 pattern、
// mutate 块按模型建议（可用时）。**生成配置本身不依赖 AI**——AI 不可用就只给纯解析配置。
func (h *Handler) GrokLogstash(c *gin.Context) {
	connID := shared.ParseID(c)
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	var req grokLogstashReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	patterns := normalizePatterns(req.Patterns, req.Pattern)
	if len(patterns) == 0 {
		resp.Fail(c, esCodeGrokInvalid, "请先填写 grok pattern")
		return
	}
	if len(patterns) > grokMaxPatterns {
		resp.Fail(c, esCodeGrokInvalid, "pattern 条数最多 "+strconv.Itoa(grokMaxPatterns)+" 条")
		return
	}
	// tag / source 会被直接渲染进配置文本，含引号或换行会产出「语法坏掉但不报错」的配置，先拒掉
	tag, msg := normalizeTagOrSource(req.Tag, "tag", grokLogstashTagMax)
	if msg != "" {
		resp.Fail(c, esCodeGrokInvalid, msg)
		return
	}
	source, msg := normalizeTagOrSource(req.Source, "待解析字段", grokLogstashSourceMax)
	if msg != "" {
		resp.Fail(c, esCodeGrokInvalid, msg)
		return
	}
	if source == "" {
		source = "message"
	}
	// pattern 必须本身能编译：这里报的是 pattern 的问题，不是「配置生成失败」
	if _, err := grok.Cached(patterns, req.Definitions); err != nil {
		resp.Fail(c, esCodeGrokInvalid, err.Error())
		return
	}
	if n := len([]rune(req.Sample)); n > grokMaxTextRunes {
		resp.Fail(c, esCodeGrokInvalid, "样例文本过长（"+strconv.Itoa(n)+" 字符，上限 "+strconv.Itoa(grokMaxTextRunes)+"）")
		return
	}
	if n := len([]rune(req.Hint)); n > grokAIHintMax {
		resp.Fail(c, esCodeGrokInvalid, "补充要求过长（"+strconv.Itoa(n)+" 字符，上限 "+strconv.Itoa(grokAIHintMax)+"）")
		return
	}
	samples := splitSampleLines(req.Sample)
	fields := patternFields(patterns, req.Definitions, samples)

	// —— 前置校验全部通过：进入流式返回 ——
	// 这里必须走 SSE，不能同步返回：一次模型调用的耗时（几秒到几十秒）远超 axios 的 15 秒默认超时，
	// 每次请求都会以「请求超时 · POST /es/:id/grok/logstash」收场（实测踩到）。其余 AI 生成面板
	// 也全是流式通道；顺带还能推一帧进度给用户看着，而不是让按钮干转。
	// 响应头一经发出，后续失败只能以 event: error 帧回传。
	sw, ok := shared.NewSSEStream(c)
	if !ok {
		return
	}

	// AI 增强（默认开）：没配 AI 或调用失败都不影响下面照常出配置
	enhance := req.Enhance == nil || *req.Enhance
	state, reason := grokEnhanceSkipped, ""
	var gsub []grokGsub
	var droppedGsub []string
	notes := ""
	if enhance {
		// 先判「这次会不会真调模型」再推 phase：未配 AI 时马上返回，闪一帧「正在判断」反而像出错
		if aiconfig.Load(h.settings, h.cryptoS).Enabled {
			sw.Send("phase", gin.H{"message": "已交给模型判断 mutate 处理…"})
		}
		advice, st, rs := h.grokMutateAdvice(c.Request.Context(), conn, req, fields,
			limitRunes(samples, grokAISampleMax))
		state, reason = st, rs
		if advice != nil {
			notes = advice.Notes
			gsub, droppedGsub = sanitizeGsub(advice.Gsub, fields)
		}
	}

	// 删哪些字段由开关 + 该连接的「系统自带字段」缓存决定，模型不参与。
	// 清单来源见 field_probe.go：候选池 ∩ 该版本 ECS 定义 ∩ 集群里真实存在，再并上人工补充。
	dropDefaults := req.DropDefaults == nil || *req.DropDefaults
	sysFields, cacheStatus, esVersion := h.systemFields(conn.ID)
	remove := logstashRemoveList(dropDefaults, sysFields)

	sw.Send("final", gin.H{
		"config":        buildLogstashFilter(tag, source, patterns, gsub, remove),
		"tag":           tag,
		"source":        source,
		"patterns":      patterns,
		"fields":        fields,
		"remove":        remove,
		"gsub":          gsub,
		"notes":         notes,
		"drop_defaults": dropDefaults,
		// 清单的来源与新鲜度：界面据此说明「这份字段清单是怎么来的、为什么是兜底的」
		"cache_status":  cacheStatus,
		"es_version":    esVersion,
		"enhance":       state,
		"enhance_error": reason,
		// 被本地校验拦下的建议：不静默吞掉，用户才知道模型提过什么、为什么没进配置
		"dropped_gsub": droppedGsub,
		// 「不报错但会丢内容」的提醒（见 logstashWarnings）
		"warnings": logstashWarnings(fields),
	})
}

// logstashWarnings 给出「配置能跑、但会把内容删掉」这一类提醒。
//
// 目前只有一条：pattern 自己就产出了 message 字段（末尾用 %{GREEDYDATA:message} 兜底），
// 而 remove_field 里固定带 message —— 删它会把兜底内容一并删掉。
// 不阻止（删 message 是使用者的选择），但必须明说：这类错不报错、只丢数据。
func logstashWarnings(fields []string) []string {
	for _, f := range fields {
		if f == "message" {
			return []string{"pattern 会解析出 message 字段（多半是末尾的 %{GREEDYDATA:message} 兜底），" +
				"而 remove_field 里固定带 message，这会连兜底内容一起删掉；不需要兜底就把那段去掉"}
		}
	}
	return []string{}
}
