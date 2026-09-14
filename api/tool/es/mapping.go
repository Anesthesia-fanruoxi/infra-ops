// ES 数据视图探测与字段合并：_resolve/index + _field_caps + _mapping。
package es

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"infra-ops/model"
)

const (
	esFieldCapLimit = 2000 // 字段总数截断上限（§5.5）
	esWarnThreshold = 50   // 命中数 > 50 视为范围过宽（§5.2）
)

// capInfo ES _field_caps 中单个类型的单字段能力。
type capInfo struct {
	Type         string `json:"type"`
	Searchable   bool   `json:"searchable"`
	Aggregatable bool   `json:"aggregatable"`
}

// resolveIndex GET /_resolve/index/{pattern}：返回三类成员名称。
func resolveIndex(ctx context.Context, c *escClient, pattern string) (indices, aliases, streams []string, err error) {
	var payload struct {
		Indices []struct {
			Name string `json:"name"`
		} `json:"indices"`
		Aliases []struct {
			Name string `json:"name"`
		} `json:"aliases"`
		DataStreams []struct {
			Name string `json:"name"`
		} `json:"data_streams"`
	}
	status, body, e := c.do(ctx, http.MethodGet, "/_resolve/index/"+pattern, nil)
	if e != nil {
		return nil, nil, nil, e
	}
	if status != http.StatusOK {
		return nil, nil, nil, esErr(status, body, "索引匹配探测失败")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, nil, err
	}
	for _, v := range payload.Indices {
		indices = append(indices, v.Name)
	}
	for _, v := range payload.Aliases {
		aliases = append(aliases, v.Name)
	}
	for _, v := range payload.DataStreams {
		streams = append(streams, v.Name)
	}
	return indices, aliases, streams, nil
}

// indexStatsDocs GET /{pattern}/_stats/docs：命中文档总数（探测时只读使用）。
func indexStatsDocs(ctx context.Context, c *escClient, pattern string) (int64, error) {
	var payload struct {
		Indices map[string]struct {
			Total struct {
				Docs struct {
					Count int64 `json:"count"`
				} `json:"docs"`
			} `json:"total"`
		} `json:"indices"`
	}
	status, body, err := c.do(ctx, http.MethodGet, "/"+pattern+"/_stats/docs", nil)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, esErr(status, body, "获取文档数失败")
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, err
	}
	var sum int64
	for _, i := range payload.Indices {
		sum += i.Total.Docs.Count
	}
	return sum, nil
}

// fetchFieldCaps GET /{pattern}/_field_caps：返回 名->类型->能力。
func fetchFieldCaps(ctx context.Context, c *escClient, pattern string) (map[string]map[string]capInfo, error) {
	path := "/" + pattern + "/_field_caps?expand_wildcards=open&allow_no_indices=false&ignore_unavailable=false"
	status, body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "获取字段能力失败")
	}
	var payload struct {
		Fields map[string]map[string]capInfo `json:"fields"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Fields == nil {
		payload.Fields = map[string]map[string]capInfo{}
	}
	return payload.Fields, nil
}

// fetchSingleMapping GET /{index}/_mapping：单索引原始映射（排障用，不合并）。
func fetchSingleMapping(ctx context.Context, c *escClient, index string) (map[string]interface{}, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/"+index+"/_mapping", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("索引不存在")
		}
		return nil, esErr(status, body, "获取映射失败")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// mergeFields 跨成员合并字段表（§5.5）。入参为逐成员能力表（名->类型->能力）：
// 同名同类型取交；同名多类型标 type_conflict；部分成员才有则标 partial；按字段名排序稳定截断。
func mergeFields(members []map[string]map[string]capInfo) ([]model.ESViewField, bool) {
	return mergeFieldsN(members, esFieldCapLimit)
}

func mergeFieldsN(members []map[string]map[string]capInfo, limit int) ([]model.ESViewField, bool) {
	names := map[string]bool{}
	for _, caps := range members {
		for name := range caps {
			names[name] = true
		}
	}
	order := make([]string, 0, len(names))
	for name := range names {
		order = append(order, name)
	}
	sort.Strings(order)

	membersN := len(members)
	fields := make([]model.ESViewField, 0, len(order))
	for _, name := range order {
		typeSet := map[string]bool{}
		present := 0
		searchable, aggregatable := true, true
		for _, caps := range members {
			types, ok := caps[name]
			if !ok || len(types) == 0 {
				continue
			}
			present++
			for typ, ci := range types {
				typeSet[typ] = true
				if !ci.Searchable {
					searchable = false
				}
				if !ci.Aggregatable {
					aggregatable = false
				}
			}
		}
		var typeList []string
		for typ := range typeSet {
			typeList = append(typeList, typ)
		}
		sort.Strings(typeList)
		fields = append(fields, model.ESViewField{
			Path:         name,
			Types:        typeList,
			Searchable:   searchable,
			Aggregatable: aggregatable,
			TypeConflict: len(typeList) > 1,
			Partial:      present < membersN,
		})
	}
	truncated := false
	if len(fields) > limit {
		fields = fields[:limit]
		truncated = true
	}
	return fields, truncated
}

// pickTimeFields 候选时间字段：@timestamp 优先，其次名称命中 time/date/created/updated。
func pickTimeFields(fields map[string]map[string]capInfo) []string {
	var named, rest []string
	for name, types := range fields {
		if !hasDateType(types) {
			continue
		}
		if name == "@timestamp" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "time") || strings.HasPrefix(lower, "date") ||
			strings.HasPrefix(lower, "created") || strings.HasPrefix(lower, "updated") {
			named = append(named, name)
		} else {
			rest = append(rest, name)
		}
	}
	sort.Strings(named)
	sort.Strings(rest)
	out := []string{}
	if ts, ok := fields["@timestamp"]; ok && hasDateType(ts) {
		out = append(out, "@timestamp")
	}
	return append(out, append(named, rest...)...)
}

func hasDateType(types map[string]capInfo) bool {
	for t := range types {
		if t == "date" || strings.HasPrefix(t, "date_") {
			return true
		}
	}
	return false
}
