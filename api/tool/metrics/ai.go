// ai.go 监控查询工具的 AI 能力：自然语言生成 PromQL、查询结果解读，与调用记录装配。
//
// 与其他工具一致，AI 配置读平台级 settings（设置页维护），本包只做转发。
// 生成侧带一次「本地试跑 → 报错回喂 → 重试」的闭环：模型不认识这个集群的真实指标，
// 首稿常带小错，把远端报错当反馈信号比自己猜要可靠。
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiconfig"
	"infra-ops/common/aiopenai"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// 生成 PromQL 时最多送进上下文的指标数量（清单大的集群按相关度挑选）。
const promqlContextMetrics = 120

// 生成 PromQL 的标签维度上下文：多数过滤发生在标签上，只给指标名时模型会生成
// 「指标对但无过滤」的宽泛查询（实测：查"成享花项目的 app 容器"生成了
// container=~".*app.*"，漏掉 project="成享花"）。这里把标签名全集与各标签实际
// 取值（截断）一起送进上下文，并把问题里出现的实体词与标签取值对齐提示。
const (
	labelValuesMaxProbe = 24   // 最多探测多少个标签的取值
	labelValuesMaxStore = 2000 // 单标签取值最多缓存个数（内存保护）
	labelValuesConc     = 6    // 取值探测并发
	labelCtxMaxValues   = 40   // 输出到上下文时单标签最多列出几个值
	labelCtxValueMaxLen = 48   // 上下文中单个值的截断长度
	alignHintMax        = 8    // 实体对齐提示条数上限
)

// 解读时最多列出的序列条数（再多模型也看不清，统计价值趋于零）。
const digestMaxLines = 30

// 记录里文本字段的截断长度：留够排错用的信息，又不让单条记录膨胀。
const (
	logContentMax  = 4000
	logExprMax     = 2000
	logQuestionMax = 1000
	logErrMax      = 600
)

// ---------------- 提示词 ----------------

const promqlPromptHead = `你是 Prometheus / VictoriaMetrics 的 PromQL 查询助手，负责把用户的自然语言需求翻译成可直接执行的 PromQL 表达式。
当前时间：%s
规则：
1. 只输出 JSON，不要输出任何解释文字，不要用 markdown 代码块包裹；
2. 只用下面给出的指标名，绝不编造不存在的指标；
3. 计数器（counter）算速率用 rate() / irate() 配范围选择器（如 rate(http_requests_total[5m])），不要对 counter 直接求和或平均；gauge 直接选，或用 avg_over_time() / max_over_time() 等；
4. 用户提到「每秒 / 每分钟 / 每 5 分钟」这类粒度时，换算成 rate() 等函数里的范围选择器窗口；没提就默认 [5m]；查询的时间跨度由界面控制，表达式里不要写任何时间；
5. 需求涉及具体项目 / 主机 / 容器 / 服务等实体时，必须用对应标签过滤，值只能取自下方给出的标签取值（支持 =~ 模糊匹配）；列表里没有的值绝不编造；需求就是看全局时不要加多余过滤；需要按实例拆分时用 by(instance)；
6. 取 topN 用 topk(N, ...)；占比用 sum(...) / sum(...)；聚合用 by / without；
7. 表达式必须是完整、可直接执行的一条 PromQL；
8. 用户提到想看的时间段（如「今天 / 昨天 / 本周 / 上周 / 本月 / 最近 N 分钟 / N 小时 / N 天」）时，在 range 里给出时间标记（时间窗由界面按标记设置，表达式里依然不要写时间）：最近 N 分钟 / 小时 / 天写 last_Nm / last_Nh / last_Nd（如 last_30m、last_6h、last_3d）；今天 today、昨天 yesterday、本周 this_week、上周 last_week、本月 this_month、上月 last_month；没提到时间就留空串。
输出结构：{"promql":"表达式","explain":"一句话说明这个表达式算什么、单位是什么","range":"时间范围标记，没提到时间就留空串"}`

const promqlUserPrompt = `可用指标（共 %d 个，已按与需求的相关度挑选；格式：指标名（类型）：说明）：
%s
%s
用户需求：%s`

const promqlRetryPrompt = `上一次生成的表达式在本集群执行时报错：
%s
请检查指标名与函数用法，修正后重新输出 JSON（只输出 JSON）。`

const explainPromptHead = `你是监控数据分析助手，负责解读 PromQL 查询结果的统计摘要，帮助运维判断系统当前状态。
当前时间：%s
规则：
1. 只输出 JSON，不要输出任何解释文字，不要用 markdown 代码块包裹；
2. 结论必须基于给出的统计数字，不要编造数据里没有的信息；
3. summary 给总体结论（2-3 句）；
4. findings 列出值得注意的现象（异常峰值、持续上升 / 下降、全为 0、序列间差异悬殊、数据缺失等），每条一句话；没有异常就说明运行平稳；
5. suggestions 给可操作的下一步排查建议（看什么、检查什么），一切正常时给继续观察类建议；
6. 全部用中文，简洁直接，不说空话。
输出结构：{"summary":"总体结论","findings":["发现 1","发现 2"],"suggestions":["建议 1","建议 2"]}`

const explainUserPrompt = `查询表达式：%s
查询方式：%s
时间范围：%s
步长：%s；每序列点数：约 %d
序列数：%d

每序列统计（最多列出 %d 条）：
%s

用户附加问题（可为空）：%s`

// ---------------- 模型返回结构 ----------------

type promqlReply struct {
	PromQL  string `json:"promql"`
	Explain string `json:"explain"`
	Range   string `json:"range"` // 时间范围标记（today / last_Nh 等），没提到时间为空串
}

type explainReply struct {
	Summary     string   `json:"summary"`
	Findings    []string `json:"findings"`
	Suggestions []string `json:"suggestions"`
}

// ---------------- 生成 PromQL ----------------

type aiPromQLReq struct {
	Question string `json:"question" binding:"required"`
}

// GeneratePromQL POST /api/metrics/conns/:id/ai/promql
//
// 流程：拉指标清单（带缓存）→ 按问题挑相关指标作上下文 → 生成 → 本地试跑，
// 报错回喂重试一次；仍不过时不阻拦返回，附 verify_error 由前端提示。
func (h *Handler) GeneratePromQL(c *gin.Context) {
	client, conn, ok := h.resolve(c)
	if !ok {
		return
	}
	var req aiPromQLReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	question := strings.TrimSpace(req.Question)
	if question == "" {
		resp.Fail(c, resp.CodeBadRequest, "请先描述你想看什么")
		return
	}
	aiC, aiCfg, err := aiconfig.Client(h.settings, h.cryptoS)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	ctx := c.Request.Context()
	cat, err := h.catalogOf(ctx, conn, client)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取指标清单失败: "+err.Error())
		return
	}
	if len(cat.names) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "远端没有返回任何指标，请确认地址与认证配置")
		return
	}
	picked := pickMetrics(cat.names, cat.meta, question, promqlContextMetrics)

	trace := h.beginTrace(aiTraceParams{
		Kind: model.MetricsAIKindPromQL, Conn: conn,
		Model: aiCfg.Model, BaseURL: aiCfg.BaseURL,
		MetricCount: len(picked), Question: question,
	})
	defer trace.done()

	msgs := []aiopenai.Message{
		{Role: "system", Content: fmt.Sprintf(promqlPromptHead, nowText())},
		{Role: "user", Content: fmt.Sprintf(promqlUserPrompt, len(picked), metricContext(picked, cat.meta), labelsBlock(cat, question), question)},
	}
	reply, err := h.chatGenerate(ctx, aiC, msgs, trace)
	if err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	expr := reply.PromQL
	explain := strings.TrimSpace(reply.Explain)
	rangeMark := reply.Range

	verifyErr := ""
	if _, err := client.query(ctx, expr, time.Now().UnixMilli()); err != nil {
		verifyErr = err.Error()
		// 把远端报错回喂给模型修正，只重试一次；重试失败保留首稿
		retryMsgs := append(msgs,
			aiopenai.Message{Role: "assistant", Content: replyJSON(reply)},
			aiopenai.Message{Role: "user", Content: fmt.Sprintf(promqlRetryPrompt, truncateRunes(verifyErr, 500))},
		)
		if reply2, err2 := h.chatGenerate(ctx, aiC, retryMsgs, trace); err2 == nil {
			expr, explain, rangeMark = reply2.PromQL, strings.TrimSpace(reply2.Explain), reply2.Range
			if _, err := client.query(ctx, expr, time.Now().UnixMilli()); err == nil {
				verifyErr = ""
			} else {
				verifyErr = err.Error()
			}
		}
	}
	trace.row.Expr = truncateRunes(expr, logExprMax)

	resp.OK(c, model.MetricsAIPromQLResult{
		PromQL: expr, Explain: explain, UsedMetrics: len(picked),
		VerifyError: verifyErr,
		Range:       resolveAIRange(rangeMark, time.Now()),
		Usage:       trace.usage(),
	})
}

// chatGenerate 发起一次生成调用并解析出结构；用量与原文进 trace。
func (h *Handler) chatGenerate(ctx context.Context, aiC *aiopenai.Client, msgs []aiopenai.Message, trace *aiTrace) (*promqlReply, error) {
	res, err := aiC.Chat(ctx, msgs)
	if err != nil {
		return nil, err
	}
	trace.fill(res)
	trace.row.Content = truncateRunes(res.Content, logContentMax)
	var reply promqlReply
	if err := unmarshalModelJSON(res.Content, &reply); err != nil {
		return nil, fmt.Errorf("解析模型返回失败: %w", err)
	}
	reply.PromQL = strings.TrimSpace(reply.PromQL)
	if reply.PromQL == "" {
		return nil, fmt.Errorf("模型没有生成表达式，请换个说法再试")
	}
	return &reply, nil
}

// ---------------- 时间范围识别（模型给的 range 标记 → 具体区间） ----------------

// aiRangeWindowRe 滚动窗口标记：last_30m / last_6h / last_3d。
var aiRangeWindowRe = regexp.MustCompile(`^last_(\d+)([mhd])$`)

// aiRangePresetMarks 与界面时间预设 chip 完全重合的窗口标记：命中时前端直接切对应预设
// （区间右端跟随「现在」，与手点 chip 行为一致）；其余标记统一走自定义区间。
var aiRangePresetMarks = map[string]string{
	"last_5m": "5m", "last_15m": "15m", "last_1h": "1h",
	"last_6h": "6h", "last_24h": "24h", "last_1d": "24h", "last_7d": "7d",
}

// resolveAIRange 把模型输出的时间范围标记换算成具体区间（Unix 毫秒）。
// 只认提示词里写明的标记，空串或不认识的标记返回 nil——宁可不动用户当前的时间
// 选择，也不要猜；换算基准用服务端时间（桌面应用与界面同机同时钟）。
func resolveAIRange(mark string, now time.Time) *model.MetricsAIRange {
	mark = strings.ToLower(strings.TrimSpace(mark))
	// 日历边界（左闭右开）：右端不随「现在」走（今天 / 本周 / 本月到当前时刻为止）
	switch mark {
	case "today":
		mid := startOfDay(now)
		return newAIRange(mid, now, "今天", "")
	case "yesterday":
		mid := startOfDay(now)
		return newAIRange(mid.AddDate(0, 0, -1), mid, "昨天", "")
	case "this_week":
		return newAIRange(startOfWeek(now), now, "本周", "")
	case "last_week":
		mon := startOfWeek(now)
		return newAIRange(mon.AddDate(0, 0, -7), mon, "上周", "")
	case "this_month":
		return newAIRange(startOfMonth(now), now, "本月", "")
	case "last_month":
		first := startOfMonth(now)
		return newAIRange(first.AddDate(0, -1, 0), first, "上月", "")
	}
	m := aiRangeWindowRe.FindStringSubmatch(mark)
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 || n > 10000 {
		return nil
	}
	var unitSec int64
	var unitText string
	switch m[2] {
	case "m":
		unitSec, unitText = 60, "分钟"
	case "h":
		unitSec, unitText = 3600, "小时"
	default:
		unitSec, unitText = 24*3600, "天"
	}
	spanSec := int64(n) * unitSec
	if spanSec*1000 > maxRangeSpanMs {
		return nil // 超出查询上限（90 天）的窗口直接不带，免得前端切到必然报错的区间
	}
	return newAIRange(now.Add(-time.Duration(spanSec)*time.Second), now,
		fmt.Sprintf("最近 %d %s", n, unitText), aiRangePresetMarks[mark])
}

// newAIRange 组装结果；起止换算成 Unix 毫秒。
func newAIRange(start, end time.Time, label, preset string) *model.MetricsAIRange {
	return &model.MetricsAIRange{Start: start.UnixMilli(), End: end.UnixMilli(), Label: label, Preset: preset}
}

// startOfDay 当天 00:00（本地时区，与前端日期控件口径一致）。
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// startOfWeek 本周一 00:00（中文习惯以周一为周首）。
func startOfWeek(t time.Time) time.Time {
	off := (int(t.Weekday()) + 6) % 7 // 周一 = 0 … 周日 = 6
	return startOfDay(t).AddDate(0, 0, -off)
}

// startOfMonth 本月 1 日 00:00。
func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// ---------------- 解读查询结果 ----------------

type aiExplainReq struct {
	Expr     string `json:"expr" binding:"required"`
	Mode     string `json:"mode"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Step     int64  `json:"step"`
	Question string `json:"question"` // 可选：用户想特别关注的点
}

// ExplainResult POST /api/metrics/conns/:id/ai/explain
//
// 后端重跑一遍查询再解读：解读必须基于当前真实数据，不能信前端回传的结果快照。
func (h *Handler) ExplainResult(c *gin.Context) {
	client, conn, ok := h.resolve(c)
	if !ok {
		return
	}
	var req aiExplainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.Expr = strings.TrimSpace(req.Expr)
	if req.Expr == "" {
		resp.Fail(c, resp.CodeBadRequest, "查询表达式不能为空")
		return
	}
	aiC, aiCfg, err := aiconfig.Client(h.settings, h.cryptoS)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	ctx := c.Request.Context()
	res, err := runQuery(ctx, client, queryReq{
		Expr: req.Expr, Mode: req.Mode, Start: req.Start, End: req.End, Step: req.Step,
	})
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "重新执行查询失败: "+err.Error())
		return
	}
	if len(res.Series) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "查询没有返回任何数据，请先确认表达式与时间范围")
		return
	}
	digest := buildDigest(res, digestMaxLines)

	question := strings.TrimSpace(req.Question)
	trace := h.beginTrace(aiTraceParams{
		Kind: model.MetricsAIKindExplain, Conn: conn,
		Model: aiCfg.Model, BaseURL: aiCfg.BaseURL,
		MetricCount: len(res.Series), Question: question,
	})
	defer trace.done()

	stepText := "—"
	points := 1
	if res.Mode == "range" {
		stepText = strconv.FormatInt(res.Step, 10) + "s"
		if len(res.Series) > 0 {
			points = len(res.Series[0].Points)
		}
	}
	userMsg := fmt.Sprintf(explainUserPrompt,
		res.Expr, modeText(res), rangeText(res), stepText, points,
		len(res.Series), digestMaxLines, strings.Join(digest.Lines, "\n"), question)
	aiRes, err := aiC.Chat(ctx, []aiopenai.Message{
		{Role: "system", Content: fmt.Sprintf(explainPromptHead, nowText())},
		{Role: "user", Content: userMsg},
	})
	if err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	trace.fill(aiRes)
	trace.row.Content = truncateRunes(aiRes.Content, logContentMax)
	trace.row.Expr = truncateRunes(res.Expr, logExprMax)

	var reply explainReply
	if err := unmarshalModelJSON(aiRes.Content, &reply); err != nil {
		trace.fail(err)
		resp.Fail(c, resp.CodeInternal, "解析模型返回失败: "+err.Error())
		return
	}
	resp.OK(c, model.MetricsAIExplainResult{
		Summary:     strings.TrimSpace(reply.Summary),
		Findings:    orEmptyStrings(reply.Findings),
		Suggestions: orEmptyStrings(reply.Suggestions),
		Digest:      digest,
		Usage:       trace.usage(),
	})
}

// ---------------- 结果摘要（解读的输入） ----------------

// buildDigest 把查询结果整理成统计摘要：序列数 + 每序列一行可读统计。
func buildDigest(res *model.MetricsQueryResult, maxLines int) model.MetricsQueryDigest {
	d := model.MetricsQueryDigest{SeriesCount: len(res.Series), Lines: make([]string, 0, maxLines)}
	for i, s := range res.Series {
		if i >= maxLines {
			d.Truncated = true
			break
		}
		d.Lines = append(d.Lines, seriesLine(s, res.Mode))
	}
	return d
}

// seriesLine 单序列统计行：末值 / 最小 / 最大 / 均值（只看非缺失点），区间查询再附趋势与缺失数。
func seriesLine(s model.MetricsSeries, mode string) string {
	label := labelText(s.Metric)
	vals := make([]float64, 0, len(s.Points))
	missing := 0
	var last *float64
	for _, p := range s.Points {
		if p.V == nil {
			missing++
			continue
		}
		vals = append(vals, *p.V)
		last = p.V // 点按时间升序，最后一个非缺失即末值
	}
	if len(vals) == 0 {
		return label + "：全部点缺失"
	}
	min, max, sum := vals[0], vals[0], 0.0
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	line := fmt.Sprintf("%s：末值=%s 最小=%s 最大=%s 均值=%s",
		label, fmtVal(*last), fmtVal(min), fmtVal(max), fmtVal(sum/float64(len(vals))))
	if mode == "range" {
		line += " 趋势=" + trendText(vals)
		if missing > 0 {
			line += fmt.Sprintf(" 缺失=%d/%d", missing, len(s.Points))
		}
	}
	return line
}

// trendText 趋势判定：后半段均值相对前半段变化超过 ±10% 判升 / 降。
func trendText(vals []float64) string {
	if len(vals) < 4 {
		return "平稳"
	}
	half := len(vals) / 2
	f, s := mean(vals[:half]), mean(vals[half:])
	diff := s - f
	if math.Abs(diff) < 1e-9 {
		return "平稳"
	}
	base := math.Abs(f)
	if base < 1e-9 {
		base = math.Abs(s)
	}
	rel := diff / base
	switch {
	case rel > 0.1:
		return "上升"
	case rel < -0.1:
		return "下降"
	default:
		return "平稳"
	}
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// labelText 标签集渲染成 {k="v", ...}（键有序，便于阅读与稳定）。
func labelText(m map[string]string) string {
	if len(m) == 0 {
		return "(无标签)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(m[k])
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// fmtVal 数值的紧凑表达（大数走科学计数，小数保留足够有效位）。
func fmtVal(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}

func modeText(res *model.MetricsQueryResult) string {
	if res.Mode == "instant" {
		return "instant（当前时刻取值）"
	}
	return "range（区间趋势）"
}

func rangeText(res *model.MetricsQueryResult) string {
	const layout = "2006-01-02 15:04:05"
	if res.Mode == "instant" {
		return time.UnixMilli(res.Start).Format(layout)
	}
	return time.UnixMilli(res.Start).Format(layout) + " ~ " + time.UnixMilli(res.End).Format(layout)
}

// ---------------- 指标上下文挑选 ----------------

// catalogTTL 指标清单缓存有效期：清单大的集群有几百 KB，连续生成时不必每次拉。
const catalogTTL = 5 * time.Minute

// catalogCache 按连接缓存指标清单与标签维度；并发安全，零值可用。
type catalogCache struct {
	mu sync.Mutex
	m  map[int64]*cachedCatalog
}

type cachedCatalog struct {
	names  []string
	meta   map[string][]metricMeta
	labels []string            // 标签名全集
	values map[string][]string // 标签名 → 实际取值（截断存储）
	totals map[string]int      // 标签名 → 取值总数（截断前）
	at     time.Time
}

// catalogOf 取指标清单 + 元信息 + 标签维度；后两者拿不到不影响生成（有名字也能用）。
func (h *Handler) catalogOf(ctx context.Context, conn *model.MetricsConn, client *promClient) (*cachedCatalog, error) {
	if cat, ok := h.catalog.get(conn.ID); ok {
		return cat, nil
	}
	names, err := client.metricNames(ctx)
	if err != nil {
		return nil, err
	}
	meta, _ := client.metadata(ctx)
	labels, _ := client.labelNames(ctx)
	values, totals := fetchLabelValues(ctx, client, labels)
	cat := &cachedCatalog{names: names, meta: meta, labels: labels, values: values, totals: totals, at: time.Now()}
	h.catalog.set(conn.ID, cat)
	return cat, nil
}

func (c *catalogCache) get(id int64) (*cachedCatalog, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok || time.Since(e.at) > catalogTTL {
		return nil, false
	}
	return e, true
}

func (c *catalogCache) set(id int64, cat *cachedCatalog) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[int64]*cachedCatalog{}
	}
	c.m[id] = cat
}

func (c *catalogCache) drop(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, id)
}

// fetchLabelValues 并行拉取各标签的取值列表（有界并发）；单项失败或为空即放弃该项，
// 不阻断整体。存储截断到 labelValuesMaxStore，并记录截断前的总数。
func fetchLabelValues(ctx context.Context, client *promClient, labels []string) (map[string][]string, map[string]int) {
	if len(labels) == 0 {
		return nil, nil
	}
	if len(labels) > labelValuesMaxProbe {
		labels = prioritizeLabels(labels)[:labelValuesMaxProbe]
	}
	values := make(map[string][]string, len(labels))
	totals := make(map[string]int, len(labels))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, labelValuesConc)
	for _, name := range labels {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			vals, err := client.labelValues(ctx, name)
			if err != nil || len(vals) == 0 {
				return
			}
			mu.Lock()
			totals[name] = len(vals)
			if len(vals) > labelValuesMaxStore {
				vals = vals[:labelValuesMaxStore]
			}
			values[name] = vals
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return values, totals
}

// prioritizeLabels 把 instance / job 提到最前（实体过滤最常用的两个维度），其余保持原序。
func prioritizeLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, want := range [2]string{"instance", "job"} {
		for _, l := range labels {
			if l == want {
				out = append(out, l)
			}
		}
	}
	for _, l := range labels {
		if l != "instance" && l != "job" {
			out = append(out, l)
		}
	}
	return out
}

// labelsBlock 拼装标签上下文段落：标签全集 + 各标签取值 + 问题实体对齐提示。
// 远端没有标签信息时返回空串，生成行为退回「只有指标清单」的旧样。
func labelsBlock(cat *cachedCatalog, question string) string {
	if len(cat.labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("本集群可用标签：")
	b.WriteString(strings.Join(cat.labels, "、"))
	b.WriteString("\n各标签实际取值（过滤条件的值只能取自下列列表，或用 =~ 在其上做模糊匹配）：\n")
	for _, name := range cat.labels {
		vals := cat.values[name]
		if len(vals) == 0 {
			continue
		}
		total := cat.totals[name]
		if total == 0 {
			total = len(vals)
		}
		shown := vals
		if len(shown) > labelCtxMaxValues {
			shown = shown[:labelCtxMaxValues]
		}
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString(fmt.Sprintf("（共 %d 个）：", total))
		for i, v := range shown {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(truncateRunes(v, labelCtxValueMaxLen))
		}
		if len(vals) > len(shown) {
			b.WriteString(" …")
		}
		b.WriteByte('\n')
	}
	if hints := alignHints(question, cat); len(hints) > 0 {
		b.WriteString("需求中提到的实体的对应标签取值（请优先用于过滤）：\n")
		for _, h := range hints {
			b.WriteString("- ")
			b.WriteString(h)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// asciiWords 问题里「像实体名」的英文词：字母起头、总长 ≥3，容忍 - . _ 与数字。
var asciiWords = regexp.MustCompile(`[A-Za-z][A-Za-z0-9._-]{2,}`)

// alignHints 把问题里出现的实体与标签取值对齐，产出「词 → 标签=值」提示：
// ① 取值整体出现在问题文本里（对中文值如 project="成享花" 与完整主机名友好）；
// ② 问题里的英文词是某取值的一部分（对「web-01」↔「web-01:9100」这类前缀写法友好）。
func alignHints(question string, cat *cachedCatalog) []string {
	if question == "" || len(cat.values) == 0 {
		return nil
	}
	lowerQ := strings.ToLower(question)
	var words []string
	for _, w := range asciiWords.FindAllString(question, -1) {
		words = append(words, strings.ToLower(w))
	}
	hints := make([]string, 0, alignHintMax)
	seen := map[string]bool{}
	for _, name := range cat.labels {
		for _, v := range cat.values[name] {
			if len(hints) >= alignHintMax {
				return hints
			}
			if !alignableValue(v) {
				continue
			}
			lv := strings.ToLower(v)
			hit := strings.Contains(lowerQ, lv)
			if !hit {
				for _, w := range words {
					if strings.Contains(lv, w) {
						hit = true
						break
					}
				}
			}
			key := name + "\x00" + lv
			if !hit || seen[key] {
				continue
			}
			seen[key] = true
			hints = append(hints, fmt.Sprintf("「%s」→ %s=%q", v, name, v))
		}
	}
	return hints
}

// alignableValue 太短的值参与对齐只会制造噪音：纯 ASCII 要求 ≥3 字符，含中文的 ≥2 字符。
func alignableValue(v string) bool {
	ascii := true
	n := 0
	for _, r := range v {
		n++
		if r > 127 {
			ascii = false
		}
	}
	if n < 2 {
		return false
	}
	return !ascii || len(v) >= 3
}

// pickMetrics 挑出与问题最相关的指标：名字命中权重高，help 文本命中次之；
// 全 0 分（问题与指标名完全对不上）时按名字序取前 max 个，至少给模型一批干净的名字。
func pickMetrics(names []string, meta map[string][]metricMeta, question string, max int) []string {
	if len(names) <= max {
		return names
	}
	kws := keywords(question)
	helps := make(map[string]string, len(names))
	for _, n := range names {
		helps[n] = helpText(meta[n])
	}

	type scored struct {
		name  string
		score int
	}
	list := make([]scored, 0, len(names))
	for _, n := range names {
		lower := strings.ToLower(n)
		score := 0
		for _, k := range kws {
			if strings.Contains(lower, k) {
				score += len(k) + 4 // 名字命中：优先
				continue
			}
			if strings.Contains(helps[n], k) {
				score += len(k)
			}
		}
		list = append(list, scored{n, score})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].name < list[j].name
	})
	out := make([]string, 0, max)
	for i := 0; i < max && i < len(list); i++ {
		out = append(out, list[i].name)
	}
	return out
}

// helpText 指标的 help 文本拼接（小写，匹配用）。
func helpText(entries []metricMeta) string {
	var b strings.Builder
	for _, e := range entries {
		if e.Help == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToLower(e.Help))
	}
	return b.String()
}

// metricContext 把选中的指标拼成紧凑上下文（指标名（类型）：说明）。
func metricContext(picked []string, meta map[string][]metricMeta) string {
	var b strings.Builder
	for _, n := range picked {
		b.WriteString("- ")
		b.WriteString(n)
		if entries := meta[n]; len(entries) > 0 {
			t := strings.TrimSpace(entries[0].Type)
			h := strings.TrimSpace(entries[0].Help)
			if t != "" {
				b.WriteString("（")
				b.WriteString(t)
				b.WriteString("）")
			}
			if h != "" {
				b.WriteString("：")
				b.WriteString(truncateRunes(h, 160))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// monitorTermMap 常见运维中文词 → 指标名 / help 里会出现的英文词。
// 用户的问题多是中文、指标名多是英文，没有这层桥接，关键词匹配几乎全落空。
var monitorTermMap = map[string][]string{
	"内存":  {"memory", "mem"},
	"磁盘":  {"disk", "filesystem", "fs", "device"},
	"硬盘":  {"disk", "device"},
	"网络":  {"network", "net"},
	"网卡":  {"network", "nic"},
	"流量":  {"bytes", "traffic", "bandwidth"},
	"带宽":  {"bandwidth", "bytes"},
	"延迟":  {"latency", "duration", "lag"},
	"响应":  {"response", "latency", "duration"},
	"错误":  {"error", "fail"},
	"失败":  {"fail", "error"},
	"请求":  {"request", "requests"},
	"连接":  {"connection", "conn", "tcp"},
	"队列":  {"queue", "pending"},
	"线程":  {"thread"},
	"进程":  {"process", "proc"},
	"负载":  {"load", "loadavg"},
	"使用率": {"usage", "util", "utilization", "percent", "ratio"},
	"利用率": {"usage", "util", "utilization", "percent", "ratio"},
	"空间":  {"space", "avail", "free", "size"},
	"剩余":  {"avail", "free", "available"},
	"容量":  {"size", "capacity", "total"},
	"吞吐":  {"throughput", "bytes", "ops"},
	"重启":  {"restart", "start_time"},
	"存活":  {"up", "alive"},
	"状态":  {"status", "state", "up"},
	"打开":  {"open", "fd"},
	"文件":  {"file", "fd", "filesystem"},
	"副本":  {"replica", "replicas"},
	"分区":  {"partition", "lag"},
	"消费":  {"consume", "lag", "offset"},
	"生产":  {"produce", "producer", "offset"},
}

// keywords 切出用于匹配的关键词：连续英文 / 数字算整词，中文取 2-gram，
// 并把中文段落里命中的运维词展开成英文关键词（见 monitorTermMap）。
func keywords(q string) []string {
	out := []string{}
	seen := map[string]bool{}
	var ascii, cjk []rune

	add := func(w string) {
		if w == "" || seen[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	flushASCII := func() {
		if len(ascii) >= 2 {
			add(strings.ToLower(string(ascii)))
		}
		ascii = nil
	}
	flushCJK := func() {
		seg := string(cjk)
		for i := 0; i+2 <= len(cjk); i++ {
			add(string(cjk[i : i+2]))
		}
		for zh, ens := range monitorTermMap {
			if strings.Contains(seg, zh) {
				for _, en := range ens {
					add(en)
				}
			}
		}
		cjk = nil
	}
	for _, r := range q {
		switch {
		case r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			flushCJK()
			ascii = append(ascii, r)
		case r >= 0x4E00 && r <= 0x9FFF:
			flushASCII()
			cjk = append(cjk, r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return out
}

// ---------------- 调用记录装配 ----------------

// aiTrace 一次 AI 调用链的记录装配；fill 累加（生成侧可能重试，一轮多次调用），
// done 幂等落库（调用方 defer 兜底）。
type aiTrace struct {
	h      *Handler
	row    model.AILog
	start  time.Time
	calls  int
	status string
	reason string
	saved  bool
}

type aiTraceParams struct {
	Kind        string
	Conn        *model.MetricsConn
	Model       string
	BaseURL     string
	MetricCount int
	Question    string
}

func (h *Handler) beginTrace(p aiTraceParams) *aiTrace {
	row := model.AILog{
		Menu: model.AIMenuMetrics,
		Kind: p.Kind, Model: p.Model, BaseURL: p.BaseURL,
		MetricCount: p.MetricCount,
		Question:    truncateRunes(p.Question, logQuestionMax),
		Status:      model.LogStatusOK,
	}
	if p.Conn != nil {
		row.ConnID = p.Conn.ID
		row.ConnName = p.Conn.Name
	}
	return &aiTrace{h: h, start: time.Now(), row: row, status: model.LogStatusOK}
}

// fill 累加一次调用的用量；服务端回显了模型名则覆盖（多模型网关下更准）。
// 只要有一批的用量是估算的，整体就按估算标注 —— 宁可标低可信度，也不假称精确。
func (t *aiTrace) fill(res *aiopenai.Result) {
	if res == nil {
		return
	}
	t.calls++
	if res.Model != "" {
		t.row.Model = res.Model
	}
	t.row.PromptTokens += res.Usage.PromptTokens
	t.row.CompletionTokens += res.Usage.CompletionTokens
	t.row.TotalTokens += res.Usage.TotalTokens
	if !res.Usage.FromAPI {
		t.row.TokenSource = model.TokenSourceEstimated
	} else if t.row.TokenSource == "" {
		t.row.TokenSource = model.TokenSourceAPI
	}
}

// fail 标注这次调用最终没成功（模型报错、返回无法解析等）。
func (t *aiTrace) fail(err error) {
	if err == nil {
		return
	}
	t.status = model.LogStatusError
	if t.reason == "" {
		t.reason = err.Error()
	}
}

func (t *aiTrace) done() {
	if t.saved {
		return
	}
	t.saved = true
	if t.h.aiLogRepo == nil { // 未装配统一记录仓储时直接跳过，不影响调用结果
		return
	}
	t.row.ElapsedMs = time.Since(t.start).Milliseconds()
	t.row.Status = t.status
	t.row.Error = truncateRunes(t.reason, logErrMax)
	_, _ = t.h.aiLogRepo.Add(&t.row)
}

// usage 把本次用量整理成下发给前端的结构。
func (t *aiTrace) usage() model.MetricsAIUsage {
	return model.MetricsAIUsage{
		Calls:            t.calls,
		PromptTokens:     int64(t.row.PromptTokens),
		CompletionTokens: int64(t.row.CompletionTokens),
		TotalTokens:      int64(t.row.TotalTokens),
		Source:           t.row.TokenSource,
		ElapsedMs:        time.Since(t.start).Milliseconds(),
	}
}

// ---------------- 小工具 ----------------

// replyJSON 把首稿回灌为 assistant 消息（重试对话用）。
func replyJSON(reply *promqlReply) string {
	b, err := json.Marshal(reply)
	if err != nil {
		return ""
	}
	return string(b)
}

// unmarshalModelJSON 解析模型返回的 JSON；容忍 markdown 代码块包裹与前后杂字。
func unmarshalModelJSON(text string, out interface{}) error {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(strings.TrimSpace(s), "json")
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return fmt.Errorf("返回内容里没有 JSON")
	}
	closer := byte(']')
	if s[start] == '{' {
		closer = '}'
	}
	end := strings.LastIndexByte(s, closer)
	if end <= start {
		return fmt.Errorf("返回的 JSON 结构不完整")
	}
	return json.Unmarshal([]byte(s[start:end+1]), out)
}

func truncateRunes(s string, n int) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// nowText 当前时间的中文表达（带星期）。
func nowText() string {
	weekdays := [...]string{"日", "一", "二", "三", "四", "五", "六"}
	now := time.Now()
	return now.Format("2006-01-02 15:04:05") + " 星期" + weekdays[int(now.Weekday())]
}

func orEmptyStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
