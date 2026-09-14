// 数据视图探测/合并：把一次 index pattern 探测归纳为统一的 collate 结果，
// 供 probe（不落库）、视图创建/刷新（落库）、后台同步三处复用。
package es

import (
	"context"
	"strings"

	"infra-ops/model"
)

// collateResult 一次 pattern 探测的综合结果。
type collateResult struct {
	Indices        []string
	Aliases        []string
	Streams        []string
	DocsCount      int64
	Fields         []model.ESViewField // 跨成员合并、按名排序、可能截断
	Truncated      bool
	TimeCandidates []string // 候选时间字段（@timestamp 优先）
}

// collateView 探测 index pattern：解析成员 → 逐成员 _field_caps → 合并。
// pattern 未命中任何索引/数据流时返回 esCodeIndexNoMatch 错误。
func collateView(ctx context.Context, c *escClient, pattern string) (*collateResult, error) {
	indices, aliases, streams, err := resolveIndex(ctx, c, pattern)
	if err != nil {
		return nil, err
	}
	if len(indices) == 0 && len(streams) == 0 {
		return nil, &esViewErr{code: esCodeIndexNoMatch, msg: "索引匹配未命中任何索引或数据流"}
	}

	// 逐成员拉字段能力，计算 partial / type_conflict 才准确。
	var members []map[string]map[string]capInfo
	for _, idx := range indices {
		f, err := fetchFieldCaps(ctx, c, idx)
		if err != nil {
			return nil, err
		}
		members = append(members, f)
	}
	for _, st := range streams {
		f, err := fetchFieldCaps(ctx, c, st)
		if err != nil {
			return nil, err
		}
		members = append(members, f)
	}

	fields, truncated := mergeFields(members)
	candidates := pickTimeFieldsFromFields(fields)

	docs, err := indexStatsDocs(ctx, c, pattern)
	if err != nil {
		docs = 0 // 文档数仅作概览，拉取失败不阻断探测
	}

	return &collateResult{
		Indices: indices, Aliases: aliases, Streams: streams,
		DocsCount: docs, Fields: fields, Truncated: truncated,
		TimeCandidates: candidates,
	}, nil
}

// pickTimeFieldsFromFields 从合并字段表中挑选候选时间字段：
// @timestamp 置顶，其次名称命中 time/date/created/updated，最后其余 date 类型字段。
func pickTimeFieldsFromFields(fields []model.ESViewField) []string {
	var named, rest []string
	for i := range fields {
		f := &fields[i]
		if !containsDateType(f.Types) {
			continue
		}
		if f.Path == "@timestamp" {
			continue
		}
		lower := strings.ToLower(f.Path)
		if strings.HasPrefix(lower, "time") || strings.HasPrefix(lower, "date") ||
			strings.HasPrefix(lower, "created") || strings.HasPrefix(lower, "updated") {
			named = append(named, f.Path)
		} else {
			rest = append(rest, f.Path)
		}
	}
	// 保序：named/rest 已按字段名升序，符合确定性。
	out := []string{}
	for i := range fields {
		if fields[i].Path == "@timestamp" && containsDateType(fields[i].Types) {
			out = append(out, "@timestamp")
			break
		}
	}
	out = append(out, append(named, rest...)...)
	return out
}

func containsDateType(types []string) bool {
	for _, t := range types {
		if t == "date" || strings.HasPrefix(t, "date_") {
			return true
		}
	}
	return false
}

// esViewErr 附带业务错误码的错误，供 handler 映射 4006/4009 等。
type esViewErr struct {
	code int
	msg  string
}

func (e *esViewErr) Error() string { return e.msg }
