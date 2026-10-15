// import_analyze.go 导入分析：手动触发的一条 SSE 流水线。
//
// 为什么单独成流，而不是并进预览：预览要真读文件、跑 AI 可能要十几秒，摊在一条可读的
// 日志流里用户才知道它卡在哪一步（解压 / 抽样 / 生成）；出错也能停在出错那步，而不是
// 整个弹框失败。生成部分直接复用 runGrokGeneration —— 与 Grok 解析页签同一套。
package es

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/aiconfig"
	"infra-ops/common/resp"
)

// importAnalyzeReq 分析请求：文件列表是必须的；filter / hint / extra_samples / definitions 与导入同源。
type importAnalyzeReq struct {
	Sources      []string          `json:"sources"`
	Index        string            `json:"index"`
	Filter       string            `json:"filter"`        // 压缩包内条目过滤（空 = 全部）
	Hint         string            `json:"hint"`
	ExtraSamples []string          `json:"extra_samples"` // 上次导入被跳过的样例：合并进样例集重新生成
	Definitions  map[string]string `json:"definitions"`
}

// ImportAnalyze POST /api/es/:id/import/analyze（SSE）
//
// 帧类型：log / delta · phase · final（AI 生成，同 /grok/ai）/ error / done。
// log 帧带 kind（info / ok / warn / err）与 sub（子行，仅缩进展示），前端按步骤样式渲染；
// done 带 ready：true = 产生了可预览的结果（JSON 免解析 / pattern 生成完成）。
func (h *Handler) ImportAnalyze(c *gin.Context) {
	connID := shared.ParseID(c)
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	var req importAnalyzeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	sources, _, err := validateSources(req.Sources)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return
	}
	if n := len([]rune(req.Hint)); n > grokAIHintMax {
		resp.Fail(c, esCodeGrokInvalid, "补充要求 / 日志格式定义过长（"+fmt.Sprint(n)+" 字符，上限 "+fmt.Sprint(grokAIHintMax)+"）")
		return
	}

	sw, ok := shared.NewSSEStream(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	logf := func(kind, format string, a ...any) {
		sw.Send("log", gin.H{"text": fmt.Sprintf(format, a...), "kind": kind})
	}
	sublog := func(format string, a ...any) {
		sw.Send("log", gin.H{"text": fmt.Sprintf(format, a...), "sub": true})
	}

	// 1) 文件类型：先让用户确认「它有没有把文件认对」，再谈解析
	kind, files, stats, err := describeSources(sources, req.Filter)
	if err != nil {
		sw.Send("error", gin.H{"message": "读取文件失败: " + err.Error()})
		return
	}
	filter := strings.TrimSpace(req.Filter)
	switch kind {
	case "archive":
		if stats != nil && stats.marked > 0 {
			logf("info", "当前上传文件类型为：压缩包，共 %d 个文件；按包内过滤规则「%s」保留 %d 个、排除 %d 个",
				stats.total, filter, stats.kept, stats.marked)
		} else {
			logf("info", "当前上传文件类型为：压缩包，解压后共 %d 个文件", len(files))
		}
		logFileList(sublog, files)
	case "multi":
		if filter != "" {
			logf("info", "当前选择 %d 个文件，将按顺序读取（压缩包按「%s」过滤包内条目）", len(files), filter)
		} else {
			logf("info", "当前选择 %d 个文件，将按顺序读取", len(files))
		}
		logFileList(sublog, files)
	default:
		logf("info", "当前上传文件类型为：%s", kindLabel(kind))
	}

	// 2) 抽样：抽不到文本行说明是结构化 JSON —— 无需 grok，直接给「可预览」结论
	samples, err := sampleFromSources(ctx, sources, req.Filter, grokAISampleMax)
	if err != nil {
		sw.Send("error", gin.H{"message": "抽样失败: " + err.Error()})
		return
	}
	if len(samples) == 0 {
		switch kind {
		case "archive":
			logf("info", "包内文件为结构化数据（JSON），无需 grok 解析")
		case "multi":
			logf("info", "所选文件均为结构化数据（JSON），无需 grok 解析")
		}
		sw.Send("done", gin.H{"ready": true})
		return
	}
	logf("info", "已从文件抽取 %d 行真实样例（含缺失值行）", len(samples))
	// 重新生成场景：把上次导入被跳过的样例也并进来（样例翻倍，让模型看到漏网行的真实结构）
	if extra := normalizeExtraSamples(req.ExtraSamples); len(extra) > 0 {
		samples = append(samples, extra...)
		if len(samples) > grokAISampleMax*2 {
			samples = samples[:grokAISampleMax*2]
		}
		logf("info", "另附 %d 行上次导入被跳过的样例，合并共 %d 行一起生成", len(extra), len(samples))
	}
	if hint := strings.TrimSpace(req.Hint); hint != "" {
		logf("info", "已附带日志格式 / 补充说明（%d 字符），字段命名与拆分将以其为准", len([]rune(hint)))
	}

	// 3) AI 生成：未配置 AI 只提示并结束（没有 pattern 就没有可预览的结果）
	aiC, aiCfg, err := aiconfig.Client(h.settings, h.cryptoS)
	if err != nil {
		logf("warn", "未启用 AI，无法自动生成解析 pattern：请先到「设置」接入模型")
		sw.Send("done", gin.H{"ready": false})
		return
	}
	trace := h.beginGrokTrace(conn, aiCfg.Model, aiCfg.BaseURL, grokTraceQuestion(strings.TrimSpace(req.Hint), false))
	defer trace.done()
	logf("info", "开始调用 AI 生成解析 pattern，等待处理结果…")
	ready := h.runGrokGeneration(ctx, aiC, trace, sw, samples, strings.TrimSpace(req.Hint), req.Definitions, nil)
	sw.Send("done", gin.H{"ready": ready})
}

// normalizeExtraSamples 清洗调用方附带的额外样例（导入后收集的跳过样例）：
// 去空去重、单条截断到 importSkipSampleRunes、条数上限 grokAIExtraSampleMax。
func normalizeExtraSamples(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" || len(out) >= grokAIExtraSampleMax {
			continue
		}
		if rr := []rune(t); len(rr) > importSkipSampleRunes {
			t = string(rr[:importSkipSampleRunes])
		}
		dup := false
		for _, ex := range out {
			if ex == t {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, t)
		}
	}
	return out
}

// logFileList 把文件清单逐条落日志子行（上限 20 条，多出的折叠一行）。
func logFileList(sublog func(format string, a ...any), files []string) {
	const showMax = 20
	for i, f := range files {
		if i >= showMax {
			sublog("…其余 %d 个文件略", len(files)-showMax)
			break
		}
		sublog("%s", f)
	}
}

// describeSources 识别来源形态：单文件沿用 text/json/archive（含压缩包内文件清单与
// 过滤统计）；多文件为 multi（files 即所选文件名清单，stats 为 nil）。
func describeSources(sources []string, filter string) (string, []string, *zipStats, error) {
	if len(sources) == 1 {
		return describeSource(sources[0], filter)
	}
	names := make([]string, 0, len(sources))
	for _, p := range sources {
		names = append(names, filepath.Base(p))
	}
	return "multi", names, nil, nil
}

// describeSource 打开单文件识别形态：kind（text / json / archive）与压缩包内文件清单；
// stats 仅压缩包非 nil（条目总数 / 排除数 / 保留数）。
func describeSource(source, filter string) (string, []string, *zipStats, error) {
	src, err := openSource(source, filter)
	if err != nil {
		return "", nil, nil, err
	}
	defer src.Close()
	if z, ok := src.(*zipSource); ok {
		return "archive", fileNamesOf(src), &zipStats{total: z.total, marked: z.marked, kept: len(z.files)}, nil
	}
	return sourceKind(src), fileNamesOf(src), nil, nil
}

func kindLabel(kind string) string {
	switch kind {
	case "json":
		return "JSON（结构化数据，无需解析）"
	case "archive":
		return "压缩包"
	default:
		return "普通文本（按行解析）"
	}
}
