// grok_ai.go ES 工具：用自然语言生成 grok pattern。
//
// 口径与其他 AI 生成面板对齐：流式草稿、成功即填入；区别是**生成后必然本地实跑校验**——
// pattern 的最终验收标准是「在样例上整行命中」，模型自己判断不了（实测它会把日志格式定义里的
// 变量顺序当成字面分隔符，于是样例里并不存在的 '-' 被写进 pattern，结果是整行不匹配）。
// 校验跑本地引擎（免费、即时、可离线），首稿不命中就把失败现场回喂**一次**做修正，
// 且只在修正稿确实更优时才采纳；全部命中则一次模型调用就结束（常见情形，不额外变慢）。
//
// 支持一次「追加生成」：带上上一版 pattern 与最初的补充要求，让模型在其基础上改。
// 补充要求支持直接贴日志格式定义（nginx log_format / logback pattern / 应用打印模板）——
// 格式里变量与字段的对应关系是字段命名的权威依据，但**字面结构一律以样例为准**（写进提示词规则 4/9）。
package es

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/aiconfig"
	"infra-ops/common/aiopenai"
	"infra-ops/common/grok"
	"infra-ops/common/resp"
)

const (
	// grokAIHintMax 补充要求 / 日志格式定义的长度上限：nginx log_format、logback pattern 都较长
	grokAIHintMax    = 4000
	grokAISampleMax  = 20 // 送进模型的样例行数（多了浪费 token，少了看不出结构差异）
	grokAIPatternMax = 3  // 提示词里允许的最大 pattern 条数
	// grokAIExtraSampleMax 追加样例（导入后被跳过的行）最多并进来多少条：
	// 与常规样例 1:1 合并，总量即 40 行（grokAISampleMax*2）
	grokAIExtraSampleMax = 20
	// 追加上下文里文本字段的截断上限：只带一轮，但要防超长文本把上下文撑爆
	grokPrevExplainMax = 500
)

// grokAIReq AI 生成请求：样例日志 + 可选的补充要求（文本，或直接贴日志格式定义）。
//
// 样例来源二选一：Sample 是手贴文本（Grok 解析页签），Source 是本地文件路径（后端复用
// 导入的读取逻辑抽真实行；导入面板的自动生成走 /import/analyze，同样基于它取样）。
// 两者都空则报错；都给出时以手贴 Sample 优先（用户显式给的）。
type grokAIReq struct {
	Sample      string            `json:"sample"`
	Source      string            `json:"source"` // 导入场景：从该文件自动取样例
	Hint        string            `json:"hint"`
	Definitions map[string]string `json:"definitions"`
	// Prev 追加生成的上下文：Hint 是最初那轮补充要求（前端固定为首轮原文，不随后续轮次漂移），
	// Patterns / Explain 是最近一版生成结果。只带一轮——追加一次基本够用，上下文体积恒定。
	Prev *grokAIPrev `json:"prev"`
}

// grokAIPrev 上一轮的生成结果（追加生成用）。
type grokAIPrev struct {
	Hint     string   `json:"hint"`
	Patterns []string `json:"patterns"`
	Explain  string   `json:"explain"`
}

// grokReply 模型返回的结构。
type grokReply struct {
	Patterns    []string          `json:"patterns"`
	Pattern     string            `json:"pattern"` // 模型偶尔只给一条
	Definitions map[string]string `json:"definitions"`
	Explain     string            `json:"explain"`
	Fields      []grokAIField     `json:"fields"`
}

// grokAIField 字段说明（前端「字段解释」列表）。
type grokAIField struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

var (
	grokPatternListOnce sync.Once
	grokPatternList     string
)

// builtinPatternList 内置 pattern 清单（放进提示词让模型优先复用，而不是自造正则）。
func builtinPatternList() string {
	grokPatternListOnce.Do(func() {
		grokPatternList = strings.Join(grok.Names(), "、")
	})
	return grokPatternList
}

const grokPromptHead = `你是日志解析专家，负责把样例日志翻译成 grok pattern（Logstash / Elasticsearch 语法）。
规则：
1. 只输出 JSON，不要输出任何解释文字，不要用 markdown 代码块包裹；
2. pattern 必须能完整匹配整行（解析引擎按整行判定命中），必要时在末尾用 %%{GREEDYDATA:message} 兜底；
3. 优先复用下面的内置 pattern，不要自己写等价正则；确需补充时写进 definitions 再引用；
4. 字面结构（分隔符、空格、方括号、冒号）必须逐字符照抄样例：样例里分隔符出现在哪、有几个空格，pattern 里就必须一模一样。
   **但照抄时要按正则转义**：日志里出现的 [ ] ( ) . | { } + * ? ^ $ \ 这些字符在正则里都是元字符，
   必须写成 \[ \] \( \) \. \| \{ \} 等。漏转义的 [ 会被当成字符类开头 ——
   轻则整行不命中，重则直接编译失败（"[2026-10-10 14:20:31,123]" 这种写法的含义是「字符类」，不是字面方括号）；
   不要按日志格式定义里的变量顺序去「推断」样例里不存在的分隔符（格式定义只回答「哪个值是哪个字段」，不回答「长什么样」）；
5. 正则方言是 Go RE2：不支持后顾 (?<=...)、原子组 (?>...)、反向引用 \1，禁止使用；
6. 字段名用 snake_case 且语义明确（ts / level / logger / trace_id / message 等）；能识别的业务字段单独拆出来，不要整行塞进 message；
7. 样例行结构不一致时给多条 pattern（最多 %d 条，按顺序尝试，首条整行命中即生效）；
8. explain 用中文说明整体解析思路（简洁，2-3 句，不要写推理过程）；fields 逐个字段给出「字段名 + 一句话说明」，顺序与解析结果一致；
9. 补充要求里可能直接给出日志格式定义（nginx log_format、logback / log4j pattern、应用打印模板）：用它决定「变量 ↔ 字段」的对应与命名（如 nginx 的 $time_iso8601 对应时间戳、$status 对应状态码，logback 的 traceId 占位对应链路 ID）；格式里的字面文本（分隔符、Content-Type: 这类前缀）不进字段；样例与格式对不上时（变量缺值显示为 '-'、字段增减）一律以样例实际内容为准；
10. **容错优先（最重要）**：解析 / 导入时不匹配的行会被整条跳过，代价很大，而用户给的样例未必覆盖所有情况——日志里缺失值习惯写成 '-'（nginx 惯例）或留空。因此：
   - 凡是**可能缺失或取占位符**的字段（客户端 IP、上游地址 / 上游耗时、可选的 trace_id、请求体等），即便样例里恰好都有值，也默认用宽松 token：写 %%{NOTSPACE:client_ip} 而不是 %%{IPORHOST:client_ip}（后者一遇到 '-' 就整行不匹配）、写 %%{NOTSPACE:upstream_time} 而不是 %%{NUMBER:upstream_time}；
   - 只有在**确信该位置永远是规范值**时才用强类型 token（时间戳 %%{TIMESTAMP_ISO8601:ts}、状态码 %%{INT:status} 这类）；
   - 拿不准就宽松：宁可字段类型泛一点，也不要因为一个占位符让整行被跳过。
11. 输出前逐行自检：把 pattern 当成锚定整行的正则，在每条样例上过一遍能不能匹配。字段该拆就拆出来、别整行塌缩进 message；但每个字段选 token 时按规则 10 容错优先——可能缺失的值用 %%{NOTSPACE} / %%{DATA}，而不是强类型 token。平台会在样例上实跑校验、不命中时按失败现场回喂修正一次。
输出结构：{"patterns":["..."],"definitions":{"NAME":"正则"},"explain":"...","fields":[{"name":"ts","desc":"时间"}]}
内置 pattern：%s`

const grokUserPrompt = `样例日志（共 %d 行，空行已忽略）：
%s

补充要求或日志格式定义（可能为空；若给出 nginx log_format / logback pattern 这类格式定义，用它决定「变量 ↔ 字段」的对应与命名，但字面结构一律以样例为准）：
%s`

// grokFollowupPrompt 追加生成的第二段用户消息：上一版已作为 assistant 消息回灌，
// 这里只给「最初的补充要求 + 本轮追加要求」，避免把 patterns 再抄一遍（那部分在 assistant JSON 里）。
const grokFollowupPrompt = `最初的补充要求：%s
最近一版 pattern：见上一条 JSON 的 patterns 字段——它是按样例与要求逐步改出来的。
用户在本轮提出追加要求：%s
请在最近一版的基础上按追加要求修改，仍成立的部分保持不变，按同样的 JSON 结构输出（patterns / definitions / explain / fields 都要给全）。`

// buildGrokFollowupMessages 组装追加生成的对话尾巴（assistant 上一版 + user 追加要求）。
// 上一轮没有 pattern 时返回 nil，调用方按单轮生成处理。
func buildGrokFollowupMessages(prev *grokAIPrev, hint string) []aiopenai.Message {
	if prev == nil || len(prev.Patterns) == 0 {
		return nil
	}
	assistant, err := json.Marshal(grokReply{
		Patterns: prev.Patterns,
		Explain:  truncateRunes(strings.TrimSpace(prev.Explain), grokPrevExplainMax),
	})
	if err != nil {
		return nil
	}
	return []aiopenai.Message{
		{Role: "assistant", Content: string(assistant)},
		{Role: "user", Content: fmt.Sprintf(grokFollowupPrompt,
			orNone(truncateRunes(strings.TrimSpace(prev.Hint), grokAIHintMax)), orNone(hint))},
	}
}

// grokTraceQuestion 调用记录里的问题文本：追加生成加个前缀，翻记录时一眼能分清首轮与追加。
func grokTraceQuestion(hint string, followup bool) string {
	if !followup {
		return hint
	}
	return "追加：" + hint
}

// ---------------- 生成后的本地硬校验 ----------------

const (
	// grokRetryMissMax 回喂时最多列出多少条未命中行：完整样例本来就在上下文里，这里只补「哪几行挂了」
	grokRetryMissMax = 6
	grokRetryLineMax = 300 // 每条失败行的截断长度
)

// grokRetryPrompt 首稿在样例上不能整行命中时，把失败现场回喂做一次修正。
// 校验跑的是本地引擎（免费、即时、可离线），所以这多出来的一次模型调用只在「真没命中」时发生。
const grokRetryPrompt = `你上一条 JSON 给出的 pattern 在样例日志上无法整行匹配——解析引擎按 ^(?:...)$ 判定，pattern 必须逐字符覆盖整行（分隔符、空格、方括号都要与样例完全一致）。
%s
如果失败现场写的是「正则编译失败」，先修语法再谈结构——语法坏掉时命中数恒为 0，这时候改结构没有任何意义。
最常见的原因是字面括号没转义：日志里的 [ 直接写进正则就是字符类开头，必须写成 \[（] 写 \]，( 写 \(，只有 (?: 才是分组）；
字符类内部的 - 要放到末尾或转义成 \-，否则会被当成范围（如 [)-()] 会报 invalid character class range）。
请按上面的失败现场逐字符核对并修正；若未命中的那几行确实与其余行结构不同（另一种格式、或不该被解析的行），为它们单独给一条 pattern（patterns 数组按顺序尝试，首条整行命中生效），不要为了迁就它们把主 pattern 改坏。
**但要区分「结构不同」与「占位符」**：如果未命中的行只是某个位置取了占位符（如 '-'、空值），而其余行同一位置是真实值，这不是结构不同，不要新增 pattern，而应把该字段的强类型 token 放宽成能同时吃下真实值与占位符的宽松 token（如 %%{IPORHOST:client_ip} → %%{NOTSPACE:client_ip}、%%{NUMBER:upstream_time} → %%{NOTSPACE:upstream_time}），让同一条 pattern 覆盖两种情况。
按同样的 JSON 结构重新输出（patterns / definitions / explain / fields 都要给全）。`

// grokVerifyResult 本地实跑结果：模型给的 pattern 必须真的能在样例上整行命中。
type grokVerifyResult struct {
	Patterns []string
	Total    int
	Matched  int
	Partial  int
	Missed   []string // 未整行命中的样例行（供回喂，已控条数与长度）
	Compile  string   // 正则编译失败原因；非空时 Matched 恒为 0
}

// OK 全部样例都整行命中（编译失败一律不算通过）。
func (v grokVerifyResult) OK() bool {
	return v.Compile == "" && v.Total > 0 && v.Matched == v.Total
}

// feedback 回喂给模型的失败现场；没有可回喂内容时返回空串。
func (v grokVerifyResult) feedback() string {
	var b strings.Builder
	if v.Compile != "" {
		fmt.Fprintf(&b, "正则编译失败：%s\n", v.Compile)
	}
	if n := v.Total - v.Matched - v.Partial; n > 0 {
		fmt.Fprintf(&b, "未能整行命中的样例行（共 %d 行，示例）：\n", n)
		for _, l := range v.Missed {
			fmt.Fprintf(&b, "- %s\n", l)
		}
	}
	return strings.TrimSpace(b.String())
}

// grokVerify 用本地引擎在样例上实跑一遍（不访问集群、不花 token）。
// 编译失败也当成「全部未命中」处理：错误信息回喂给模型，比让它猜有用得多。
func grokVerify(samples []string, defs map[string]string, reply *grokReply) grokVerifyResult {
	out := grokVerifyResult{Patterns: normalizePatterns(reply.Patterns, reply.Pattern), Total: len(samples)}
	if len(out.Patterns) == 0 {
		out.Compile = "模型没有给出 pattern"
		return out
	}
	m, err := grok.Cached(out.Patterns, defs)
	if err != nil {
		out.Compile = err.Error()
		for _, l := range samples {
			out.miss(l)
		}
		return out
	}
	for _, line := range samples {
		mm := m.Parse(line)
		switch {
		case mm == nil:
			out.miss(line)
		case mm.Partial:
			out.Partial++
			out.miss(line)
		default:
			out.Matched++
		}
	}
	return out
}

// grokRetryBetter 修正稿是否值得采纳。判据分两级 —— 先比「能不能编译」，同级再比整行命中数。
//
// 为什么要把编译单独拎出来比：编译不过的 pattern 命中数恒为 0，但那是「跑不起来」而不是「没匹配上」。
// 只比命中数的话，一份**能编译但暂未命中**的修正稿会被判成「没更优」而丢弃，用户最终拿到的仍是那份
// logstash 根本加载不了的配置 —— 实测踩到：样例里的字面方括号被照抄进正则，当成字符类后直接编译失败。
func grokRetryBetter(prev, next grokVerifyResult) bool {
	if next.Compile != "" {
		return false // 修正稿自己也编译不过：没有可采纳的理由
	}
	if prev.Compile != "" {
		return true // 首稿编译不过、这版能编译：无条件采纳，哪怕一行都没命中
	}
	return next.Matched > prev.Matched
}

// miss 记一条失败行（控条数与长度：回喂体积要可控）。
func (v *grokVerifyResult) miss(line string) {
	if len(v.Missed) >= grokRetryMissMax {
		return
	}
	v.Missed = append(v.Missed, truncateRunes(line, grokRetryLineMax))
}

// mergeDefs 用户自定义 + 模型给的定义：模型优先——它的 pattern 正是照着这些定义写的；
// 用户已有的同名项保留在 UI 里（前端只追加模型新增的定义，不覆盖用户那几行）。
func mergeDefs(user, fromModel map[string]string) map[string]string {
	out := make(map[string]string, len(user)+len(fromModel))
	put := func(m map[string]string) {
		for k, v := range m {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k != "" && v != "" {
				out[k] = v
			}
		}
	}
	put(user)
	put(fromModel)
	return out
}

// marshalReplyJSON 把上一版结果回灌成 assistant 消息（模型据此看到自己给过的 pattern）。
func marshalReplyJSON(r *grokReply) string {
	b, err := json.Marshal(grokReply{
		Patterns: normalizePatterns(r.Patterns, r.Pattern),
		Explain:  truncateRunes(strings.TrimSpace(r.Explain), grokPrevExplainMax),
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// GrokAI 生成 grok pattern：单次模型调用（流式），可选一轮追加生成。
func (h *Handler) GrokAI(c *gin.Context) {
	connID := shared.ParseID(c)
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	var req grokAIReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	// 样例来源：手贴 Sample 优先（用户显式给的），否则从 Source 文件自动抽真实行。
	// 自动取样是「用真实数据驱动生成」的关键——手贴的样例若清一色带 IP，模型就会生成只认 IP 的
	// 过严 pattern，导入时把 '-' 占位行整片跳过；从文件等距抽样能把占位行也纳入，校验才真正兜得住。
	var send []string
	switch {
	case strings.TrimSpace(req.Sample) != "":
		if len([]rune(req.Sample)) > grokMaxTextRunes {
			resp.Fail(c, esCodeGrokInvalid, "样例文本过长（上限 "+strconv.Itoa(grokMaxTextRunes)+" 字符）")
			return
		}
		samples := splitSampleLines(req.Sample)
		if len(samples) == 0 {
			resp.Fail(c, esCodeGrokInvalid, "请先贴一段样例日志")
			return
		}
		send = samples
		if len(send) > grokAISampleMax {
			send = send[:grokAISampleMax]
		}
	case strings.TrimSpace(req.Source) != "":
		lines, err := sampleFromSource(c.Request.Context(), strings.TrimSpace(req.Source), "", grokAISampleMax)
		if err != nil {
			resp.Fail(c, esCodeImportInvalid, "从文件取样例失败: "+err.Error())
			return
		}
		if len(lines) == 0 {
			resp.Fail(c, esCodeGrokInvalid, "该文件没有可取样的文本行（可能是结构化 JSON），请改用手工样例")
			return
		}
		send = lines
	default:
		resp.Fail(c, esCodeGrokInvalid, "请先选择要导入的文件，或贴一段样例日志")
		return
	}
	aiC, aiCfg, err := aiconfig.Client(h.settings, h.cryptoS)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	hint := strings.TrimSpace(req.Hint)
	if n := len([]rune(hint)); n > grokAIHintMax {
		resp.Fail(c, esCodeGrokInvalid, "补充要求 / 日志格式定义过长（"+strconv.Itoa(n)+" 字符，上限 "+strconv.Itoa(grokAIHintMax)+"）")
		return
	}
	usePrev := req.Prev != nil && len(req.Prev.Patterns) > 0

	// —— 前置校验全部通过：进入流式返回 ——
	// 响应头一经发出，后续失败只能以 event: error 帧回传。
	sw, ok := shared.NewSSEStream(c)
	if !ok {
		return
	}
	trace := h.beginGrokTrace(conn, aiCfg.Model, aiCfg.BaseURL, grokTraceQuestion(hint, usePrev))
	defer trace.done()

	// 生成与「导入分析」共用同一套生成流程（那边的差异只是前置旁白）
	h.runGrokGeneration(c.Request.Context(), aiC, trace, sw, send, hint, req.Definitions, req.Prev)
}

// runGrokGeneration 跑「组提示词 → 流式生成 → 本地实跑校验 → 失败回喂一次 → final 帧」的完整流程。
//
// AI 生成（Grok 解析页签）与导入分析（/import/analyze）共用：两者只是「样例从哪来、前置旁白说什么」
// 不同，生成本身是同一套 —— 复制一份迟早改了一边漏了另一边。
// samples 是一行一条的样例行；hint 是补充要求 / 日志格式定义；defs 是用户自定义 pattern 定义。
// 返回值 = 是否产出了终稿（final 帧）；导入分析据此决定 done 的 ready（没终稿就没有可预览的结果）。
func (h *Handler) runGrokGeneration(ctx context.Context, aiC *aiopenai.Client, trace *grokTrace,
	sw *shared.SSEStream, samples []string, hint string, defs map[string]string, prev *grokAIPrev) bool {

	msgs := []aiopenai.Message{
		{Role: "system", Content: fmt.Sprintf(grokPromptHead, grokAIPatternMax, builtinPatternList())},
		{Role: "user", Content: fmt.Sprintf(grokUserPrompt, len(samples), strings.Join(samples, "\n"), orNone(hint))},
	}
	// 追加生成：把上一版 pattern 与追加要求接在首轮对话之后（只带一轮，上下文体积恒定）
	msgs = append(msgs, buildGrokFollowupMessages(prev, hint)...)

	// 流式草稿：patterns 数组逐条 + explain 的字符串增量边生成边推；
	// 模型偶尔只给一条 pattern（单字符串字段），同样按 patterns[0] 推送
	emit := func(ev shared.JSONStringEvent) {
		switch {
		case ev.Field == "patterns" && ev.Index >= 0:
			sw.Send("delta", gin.H{"field": "patterns", "index": ev.Index, "text": ev.Delta})
		case ev.Field == "pattern" && ev.Index < 0:
			sw.Send("delta", gin.H{"field": "patterns", "index": 0, "text": ev.Delta})
		case ev.Field == "explain" && ev.Index < 0:
			sw.Send("delta", gin.H{"field": "explain", "text": ev.Delta})
		}
	}
	reply, err := h.grokChat(ctx, aiC, msgs, trace, emit)
	if err != nil {
		trace.fail(err)
		sw.Send("error", gin.H{"message": err.Error()})
		return false
	}

	// 本地硬校验：模型给的 pattern 必须真的能在样例上整行命中。
	// 挡的正是「按格式定义推断出样例里并不存在的分隔符」这类错误——模型自己看不出来，跑一遍就知道；
	// 校验走本地引擎（免费、即时、可离线），所以「不命中就回喂一次」的额外模型调用只在真没命中时发生。
	verify := grokVerify(samples, mergeDefs(defs, reply.Definitions), reply)
	attempts, adopted := 1, false
	if !verify.OK() {
		fail := verify.feedback()
		sw.Send("phase", gin.H{"stage": "retry", "message": fail, "matched": verify.Matched, "total": verify.Total})
		retryMsgs := append(append([]aiopenai.Message{}, msgs...),
			aiopenai.Message{Role: "assistant", Content: marshalReplyJSON(reply)},
			aiopenai.Message{Role: "user", Content: fmt.Sprintf(grokRetryPrompt, fail)},
		)
		attempts = 2
		if r2, err2 := h.grokChat(ctx, aiC, retryMsgs, trace, emit); err2 == nil {
			v2 := grokVerify(samples, mergeDefs(defs, r2.Definitions), r2)
			// 采纳判据分两级：先比「能不能编译」，同级才比命中数（见 grokRetryBetter）
			if grokRetryBetter(verify, v2) {
				reply, verify, adopted = r2, v2, true
			}
		}
	}

	patterns := verify.Patterns
	trace.row.Expr = truncateRunes(strings.Join(patterns, " ⏎ "), 2000)
	sw.Send("final", gin.H{
		"patterns":    patterns,
		"definitions": reply.Definitions,
		"explain":     strings.TrimSpace(reply.Explain),
		"fields":      reply.Fields,
		"verify": gin.H{
			"ok": verify.OK(), "matched": verify.Matched, "partial": verify.Partial,
			"missed": verify.Total - verify.Matched - verify.Partial, "total": verify.Total,
			"attempts": attempts, "adopted_retry": adopted, "error": verify.Compile,
		},
		"usage": trace.usage(),
	})
	return true
}

// grokChat 一次模型调用 + JSON 解析；原文与用量进 trace。
// onDelta 非空时把模型输出 JSON 的字符串增量（patterns / explain）交给调用方做流式展示。
func (h *Handler) grokChat(ctx context.Context, aiC *aiopenai.Client, msgs []aiopenai.Message, trace *grokTrace, onDelta func(shared.JSONStringEvent)) (*grokReply, error) {
	scanner := &shared.JSONStreamScanner{}
	res, err := aiC.ChatStream(ctx, msgs, func(chunk string) error {
		if onDelta != nil {
			scanner.Feed(chunk, onDelta)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	trace.fill(res)
	var reply grokReply
	if err := unmarshalModelJSON(res.Content, &reply); err != nil {
		return nil, fmt.Errorf("解析模型返回失败: %w", err)
	}
	if len(normalizePatterns(reply.Patterns, reply.Pattern)) == 0 {
		return nil, fmt.Errorf("模型没有给出 pattern，请补充说明日志格式后再试")
	}
	return &reply, nil
}

func orNone(s string) string {
	if s == "" {
		return "（无）"
	}
	return truncateRunes(s, grokAIHintMax)
}

// unmarshalModelJSON 宽松解析模型返回：容忍 markdown 代码块包裹与前后杂质。
func unmarshalModelJSON(content string, out interface{}) error {
	s := strings.TrimSpace(content)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimPrefix(strings.TrimSpace(s), "json")
		s = strings.TrimSpace(s)
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	return json.Unmarshal([]byte(s), out)
}
