// grok_logstash.go 把「已经调通的 grok pattern」组装成可直接落地的 Logstash filter 配置。
//
// 为什么要做这一步：pattern 在解析面板里整行命中之后，真正要粘进 logstash.conf 的东西还得手写一遍——
// grok 块、match 的单字符串 / 数组两种写法、外层 `if "x" in [tags]` 条件、以及解析完清理采集器元数据的
// mutate。手写这几处最容易错的地方恰好都不报错（多 pattern 写成单字符串会被当成一条、remove_field
// 顺手把刚解析出来的业务字段删掉、pattern 里的 \d 被写成 \\d）。
//
// 分工（本文件的核心设计，改之前请先读）：
//   - **骨架一律本地拼装**：if 条件 / grok 块 / 缩进 / 引号 / 字符串与数组写法，全在 buildLogstashFilter
//     里按确定性规则生成。这一段是纯语法，交给模型去写只会引入「括号不配对整份配置加载失败」的风险。
//   - **只有 mutate 段的业务判断交给模型**：哪些占位符要替换、哪些采集器元数据该删，是经验知识；
//     但回包必须是**结构化字段**（gsub / remove_field），拼装仍由本地完成。
//   - **模型给的东西要过本地校验**：不在 pattern 字段清单里的 gsub 丢掉、与业务字段冲突的 remove_field
//     剔除——模型幻觉出 `name` / `host` 这类常见字段名并把它删掉，是最难发现的一种配置事故。
//   - **模型不可用不是错误**：调用失败退化为「纯解析配置」（只有 grok 块），响应带 enhance_error 说明，
//     配置永远可用。AI 只是加分项，不能成为生成配置的前置条件。
package es

import (
	"regexp"
	"strconv"
	"strings"

	"infra-ops/common/grok"
)

const (
	grokLogstashTagMax    = 64
	grokLogstashSourceMax = 64
	grokLogstashMaxGsub   = 12  // mutate/gsub 条数上限（再多说明该回 pattern 层解决）
	grokLogstashFieldMax  = 200 // 字段清单上限（展示与校验共用）
	grokLogstashNoteMax   = 200 // 模型 notes 截断长度
)

// logstashMessageOnly 无论开关开不开都要删的那一个：message 里的原始整行已经被 grok
// 拆成一个个字段了，留着就是一份重复数据。
var logstashMessageOnly = []string{"message"}

// grokGsub 一条字段值替换，对应 logstash mutate/gsub 的三元组（field, 正则, 替换值）。
type grokGsub struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// grokMutateAI 模型给出的 mutate 建议。只允许这两项：配置文本由本地拼装，模型不碰语法。
// **不含 remove_field** —— 要删哪些采集器字段是按固定清单 + 开关决定的（见 logstashDefaultDrop），
// 让模型每次重新推导那串名字没有意义，还容易把业务字段顺手写进去。
type grokMutateAI struct {
	Gsub  []grokGsub `json:"gsub"`
	Notes string     `json:"notes"`
}

// grokLogstashReq 生成配置的请求。patterns 为空时用 pattern 单数字段兜底（与解析接口同一套写法）。
type grokLogstashReq struct {
	Patterns    []string          `json:"patterns"`
	Pattern     string            `json:"pattern"`
	Definitions map[string]string `json:"definitions"`
	// Tag 外层条件：`if "<tag>" in [tags]`。留空则不加条件，grok 块直接挂在 filter 下。
	Tag string `json:"tag"`
	// Source 待解析字段名，默认 message（部分采集链路把原始日志放在 log / content）。
	Source  string `json:"source"`
	Sample  string `json:"sample"`  // AI 增强用；纯本地生成不需要
	Hint    string `json:"hint"`    // 补充要求（如「这是 filebeat 送的 nginx access log」）
	Enhance *bool  `json:"enhance"` // 默认 true；显式 false 时只做本地拼装
	// DropDefaults 是否连采集器自带的那串元数据字段一起删（默认删）。
	// 关掉后仍会删 message —— 原始整行已经被 grok 拆成字段了。
	DropDefaults *bool `json:"drop_defaults"`
}

// ---------------- 本地拼装 ----------------

// logstashQuote 渲染成 logstash 配置里的双引号字符串。
// 只转义双引号本身，**不动反斜杠**：grok pattern 里的 \d \s \[ 是要原样传进正则的，
// 若一并转义成 \\d，logstash 拿到的就是「字面反斜杠 + d」，pattern 直接失配——
// 而 logstash 自己的配置解析器并不会替你把 \\d 还原回来。
func logstashQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// buildLogstashFilter 拼装完整 filter 文本。tag 为空则不加 if 包裹（内层缩进少一级）。
//
// 单条 pattern 用字符串写法（与 logstash 文档、手写习惯一致），多条才用数组——
// 数组里的顺序就是匹配顺序，与解析面板「按顺序尝试、首条整行命中生效」完全对应。
func buildLogstashFilter(tag, source string, patterns []string, gsub []grokGsub, remove []string) string {
	if source == "" {
		source = "message" // 兜底：空的待解析字段会渲染出 `"" =>` 这种坏配置
	}
	var b strings.Builder
	b.WriteString("filter {\n")
	indent, footer := "  ", ""
	if tag != "" {
		b.WriteString("  if " + logstashQuote(tag) + " in [tags] {\n")
		indent, footer = "    ", "  }\n"
	}
	b.WriteString(indent + "grok {\n")
	if len(patterns) == 1 {
		b.WriteString(indent + "  match => { " + logstashQuote(source) + " => " + logstashQuote(patterns[0]) + " }\n")
	} else {
		b.WriteString(indent + "  match => { " + logstashQuote(source) + " => [\n")
		for i, p := range patterns {
			if i < len(patterns)-1 {
				b.WriteString(indent + "    " + logstashQuote(p) + ",\n")
			} else {
				b.WriteString(indent + "    " + logstashQuote(p) + "\n")
			}
		}
		b.WriteString(indent + "  ] }\n")
	}
	b.WriteString(indent + "}\n")
	b.WriteString(renderMutate(gsub, remove, indent))
	b.WriteString(footer + "}\n")
	return b.String()
}

// renderMutate 渲染 mutate 块；没有任何可处理项时返回空串（不生成空 mutate）。
func renderMutate(gsub []grokGsub, remove []string, indent string) string {
	if len(gsub) == 0 && len(remove) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(indent + "mutate {\n")
	if len(gsub) > 0 {
		// gsub 的形参是一个平铺数组：字段名、正则、替换值依次排开
		parts := make([]string, 0, len(gsub)*3)
		for _, g := range gsub {
			parts = append(parts, logstashQuote(g.Field), logstashQuote(g.From), logstashQuote(g.To))
		}
		b.WriteString(indent + "  gsub => [" + strings.Join(parts, ", ") + "]\n")
	}
	if len(remove) > 0 {
		parts := make([]string, 0, len(remove))
		for _, f := range remove {
			parts = append(parts, logstashQuote(f))
		}
		b.WriteString(indent + "  remove_field => [" + strings.Join(parts, ", ") + "]\n")
	}
	b.WriteString(indent + "}\n")
	return b.String()
}

// ---------------- 字段清单（校验模型的依据） ----------------

// 与解析引擎同一套语法：%{NAME:field[:type]} 与手写命名组 (?<name> / (?P<name>)。
var (
	logstashFieldRefRe   = regexp.MustCompile(`%\{[A-Za-z_][A-Za-z0-9_]*:([A-Za-z_][A-Za-z0-9_.\-]*)(?::[A-Za-z]+)?\}`)
	logstashNamedGroupRe = regexp.MustCompile(`\(\?P?<([A-Za-z_][A-Za-z0-9_]*)>`)
)

// patternFields 收集 pattern 会产出的字段名。
// 先静态扫 pattern 文本，再用样例实跑补齐——%{COMBINEDAPACHELOG} 这类组合 pattern 的内部字段
// 只有展开后才知道，静态扫描是看不见的。静态在前是为了顺序接近 pattern 的书写顺序。
func patternFields(patterns []string, defs map[string]string, samples []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 16)
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] || len(out) >= grokLogstashFieldMax {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, p := range patterns {
		for _, m := range logstashFieldRefRe.FindAllStringSubmatch(p, -1) {
			add(m[1])
		}
		for _, m := range logstashNamedGroupRe.FindAllStringSubmatch(p, -1) {
			add(m[1])
		}
	}
	if m, err := grok.Cached(patterns, defs); err == nil {
		for _, line := range samples {
			if mm := m.Parse(line); mm != nil {
				for _, f := range mm.Fields {
					add(f.Name)
				}
			}
		}
	}
	return out
}

// sanitizeGsub 用「pattern 实际产出的字段」把模型给的 gsub 过滤一遍：
// 字段不在清单里 = 模型凭空造的（替换一个不存在的字段纯属噪音），丢弃。
// 丢弃项要回给前端 —— 静默吞掉的话，用户以为模型提了、实际没生效。
func sanitizeGsub(list []grokGsub, fields []string) ([]grokGsub, []string) {
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f] = true
	}
	out := make([]grokGsub, 0, len(list))
	var dropped []string
	seen := map[string]bool{}
	for _, g := range list {
		if len(out) >= grokLogstashMaxGsub {
			break
		}
		f := strings.TrimSpace(g.Field)
		if f == "" || g.From == "" {
			continue // 三元组不完整，拼进配置只会让 logstash 报错
		}
		if !known[f] {
			dropped = append(dropped, f)
			continue
		}
		if seen[f] {
			continue // 同一字段重复替换：留第一条，后面的按 logstash 顺序也只会生效一次
		}
		seen[f] = true
		out = append(out, grokGsub{Field: f, From: g.From, To: g.To})
	}
	return out, dropped
}

// logstashRemoveList 按开关给出要删的字段。meta 是「系统自带字段」（探测出来 + 人工补充）。
//
//   - 开关打开：meta 去重后生效，并**始终带上 message**；
//   - 开关关闭：只删 message —— 原始整行已经被 grok 拆成字段了，留着是重复数据。
//
// meta 为空时按「只有 message」处理，**不填默认值**：空的原因要么是还没探测过、
// 要么是这集群确实没有可删的元数据字段，两种情况下硬塞一组名字都是噪音
// （调用方在缓存未就绪时会退回候选池，见 grok_logstash_ai.go）。
func logstashRemoveList(dropDefaults bool, meta []string) []string {
	if !dropDefaults {
		return append([]string{}, logstashMessageOnly...)
	}
	out := make([]string, 0, len(meta)+1)
	seen := map[string]bool{}
	for _, f := range meta {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if !seen["message"] {
		out = append(out, "message")
	}
	return out
}

// normalizeTagOrSource 校验并归一化 tag / source：这两个值会被直接渲染进配置文本，
// 含引号或换行会破坏语法（不报错的坏配置比报错更难查），一律拒收。
func normalizeTagOrSource(v, what string, max int) (string, string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ""
	}
	if n := len([]rune(v)); n > max {
		return "", what + "过长（" + strconv.Itoa(n) + " 字符，上限 " + strconv.Itoa(max) + "）"
	}
	if strings.ContainsAny(v, " \"\\\n\r\t") {
		return "", what + "不能包含引号、反斜杠或空白字符"
	}
	return v, ""
}
