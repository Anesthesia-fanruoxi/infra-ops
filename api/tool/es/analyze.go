// 即时分析（B10，§12）：去重计数 / 图状分析。
// 分析走 ES 聚合（size:0 + aggs）统计**全量匹配集**，不是内存里那 1000 条（§12.1）；
// query 段与检索共用同一份 compileQuery 输出，保证「图上的总数」=「列表的 total」。
package es

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
)

const (
	esAnalyzeMaxFields = 10 // 去重计数字段上限
	esAnalyzeDim1Max   = 50 // 一级桶数上限
	esAnalyzeDim2Max   = 20 // 二级桶数上限
)

// analyzeBaseReq 两种分析共用的检索条件（start/end 校验同 §14.4）。
type analyzeBaseReq struct {
	ViewID int64  `json:"view_id" binding:"required"`
	KQL    string `json:"kql"`
	Start  string `json:"start" binding:"required"`
	End    string `json:"end" binding:"required"`
}

// AnalyzeDistinct 去重计数：每字段 cardinality（precision_threshold:40000）+ terms Top 值分布。
func (h *Handler) AnalyzeDistinct(c *gin.Context) {
	connID := shared.ParseID(c)
	client, code, msg := h.resolve(connID)
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		analyzeBaseReq
		Fields []string `json:"fields" binding:"required"`
	}
	if err := bindJSON(c, &req); err != nil {
		return
	}
	if len(req.Fields) == 0 || len(req.Fields) > esAnalyzeMaxFields {
		resp.Fail(c, esCodeAnalyzeInvalid, "字段数量须为 1–10 个")
		return
	}
	view, schema, derr := h.loadSchema(connID, req.ViewID)
	if derr != nil {
		failDSLErr(c, derr)
		return
	}
	startMs, endMs, terr := ParseTimeRange(req.Start, req.End)
	if terr != nil {
		resp.ErrHTTP(c, http.StatusBadRequest, esCodeTimeInvalid, "时间参数非法: "+terr.Error())
		return
	}
	body, derr := buildAnalyzeBody(schema, req.KQL, startMs, endMs, nil)
	if derr != nil {
		failDSLErr(c, derr)
		return
	}
	aggs := map[string]interface{}{}
	for i, f := range req.Fields {
		fd := schema.ByPath[f]
		if fd == nil || !fd.Aggregatable {
			resp.Fail(c, esCodeAnalyzeInvalid, "字段 "+f+" 不可聚合")
			return
		}
		aggs["f"+strconv.Itoa(i)] = map[string]interface{}{
			"cardinality": map[string]interface{}{"field": f, "precision_threshold": 40000}}
		aggs["f"+strconv.Itoa(i)+"_top"] = map[string]interface{}{
			"terms": map[string]interface{}{"field": f, "size": 50, "order": map[string]string{"_count": "desc"}}}
	}
	body["aggs"] = aggs
	executeAnalyze(c, client, view.IndexPattern, body)
}

// AnalyzeChart 图状分析：dim1 必选 / dim2 可选 / metric 必选（§12.2）。
func (h *Handler) AnalyzeChart(c *gin.Context) {
	connID := shared.ParseID(c)
	client, code, msg := h.resolve(connID)
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		analyzeBaseReq
		Dim1   analyzeDim    `json:"dim1" binding:"required"`
		Dim2   *analyzeDim   `json:"dim2"`
		Metric analyzeMetric `json:"metric"`
	}
	if err := bindJSON(c, &req); err != nil {
		return
	}
	if req.Metric.Type == "" {
		req.Metric.Type = "count"
	}
	view, schema, derr := h.loadSchema(connID, req.ViewID)
	if derr != nil {
		failDSLErr(c, derr)
		return
	}
	startMs, endMs, terr := ParseTimeRange(req.Start, req.End)
	if terr != nil {
		resp.ErrHTTP(c, http.StatusBadRequest, esCodeTimeInvalid, "时间参数非法: "+terr.Error())
		return
	}
	body, derr := buildAnalyzeBody(schema, req.KQL, startMs, endMs, nil)
	if derr != nil {
		failDSLErr(c, derr)
		return
	}
	d1, derr := buildDim(schema, &req.Dim1, esAnalyzeDim1Max, "一级维度")
	if derr != nil {
		failDSLErr(c, derr)
		return
	}
	if req.Dim2 != nil {
		d2, derr2 := buildDim(schema, req.Dim2, esAnalyzeDim2Max, "二级维度")
		if derr2 != nil {
			failDSLErr(c, derr2)
			return
		}
		m1, derr3 := buildMetric(schema, &req.Metric)
		if derr3 != nil {
			failDSLErr(c, derr3)
			return
		}
		d1["aggs"] = map[string]interface{}{
			"m1":    m1,
			"split": d2,
		}
	} else {
		m1, derr3 := buildMetric(schema, &req.Metric)
		if derr3 != nil {
			failDSLErr(c, derr3)
			return
		}
		d1["aggs"] = map[string]interface{}{"m1": m1}
	}
	body["aggs"] = map[string]interface{}{"b1": d1}
	executeAnalyze(c, client, view.IndexPattern, body)
}

// analyzeDim 分析维度。
type analyzeDim struct {
	Type     string `json:"type"` // terms | date_histogram | histogram
	Field    string `json:"field"`
	Size     int    `json:"size"`
	Interval string `json:"interval"` // histogram 数值区间
}

// analyzeMetric 分析指标。
type analyzeMetric struct {
	Type  string `json:"type"` // count|sum|avg|min|max|cardinality
	Field string `json:"field"`
}

// buildDim 校验并组装维度聚合；桶数超限 / 字段不可聚合 → 4012。
func buildDim(schema *Schema, d *analyzeDim, maxBuckets int, label string) (map[string]interface{}, *dslErr) {
	switch d.Type {
	case "terms":
		f := schema.ByPath[d.Field]
		if f == nil || !f.Aggregatable {
			return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "字段 " + d.Field + " 不可聚合"}
		}
		size := d.Size
		if size <= 0 {
			size = 20
		}
		if size > maxBuckets {
			return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "桶数超上限 " + strconv.Itoa(maxBuckets)}
		}
		return map[string]interface{}{"terms": map[string]interface{}{
			"field": d.Field, "size": size, "order": map[string]string{"_count": "desc"}}}, nil
	case "date_histogram":
		f := schema.ByPath[d.Field]
		if f == nil || !containsDateType(f.Types) {
			return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "字段 " + d.Field + " 不是 date 类型"}
		}
		if d.Interval == "" {
			return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "缺少 fixed_interval"}
		}
		return map[string]interface{}{"date_histogram": map[string]interface{}{
			"field": d.Field, "fixed_interval": d.Interval, "time_zone": "+08:00", "min_doc_count": 0}}, nil
	case "histogram":
		f := schema.ByPath[d.Field]
		if f == nil || !f.Aggregatable {
			return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "字段 " + d.Field + " 不可聚合"}
		}
		if d.Interval == "" {
			return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "缺少 interval"}
		}
		return map[string]interface{}{"histogram": map[string]interface{}{
			"field": d.Field, "interval": d.Interval, "min_doc_count": 0}}, nil
	}
	return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: label + "类型须为 terms / date_histogram / histogram"}
}

// buildMetric 组装指标聚合；指标与字段类型不符 → 4012。
func buildMetric(schema *Schema, m *analyzeMetric) (map[string]interface{}, *dslErr) {
	if m.Type == "count" {
		return map[string]interface{}{"value_count": map[string]interface{}{"field": "_id"}}, nil
	}
	switch m.Type {
	case "sum", "avg", "min", "max":
		nf := schema.ByPath[m.Field]
		if nf == nil || !nf.Aggregatable || len(nf.Types) != 1 || !numericTypes[nf.Types[0]] {
			return nil, &dslErr{code: esCodeAnalyzeInvalid,
				msg: m.Type + " 指标要求字段 " + m.Field + " 为可聚合的数值类型"}
		}
	case "cardinality":
		nf := schema.ByPath[m.Field]
		if nf == nil || !nf.Aggregatable {
			return nil, &dslErr{code: esCodeAnalyzeInvalid,
				msg: "cardinality 指标要求字段 " + m.Field + " 可聚合"}
		}
	default:
		return nil, &dslErr{code: esCodeAnalyzeInvalid, msg: "未知指标类型 " + m.Type}
	}
	return map[string]interface{}{m.Type: map[string]interface{}{"field": m.Field}}, nil
}

// buildAnalyzeBody 分析请求体：query 段与检索同源，size:0 + track_total_hits（§12.1）。
func buildAnalyzeBody(schema *Schema, kql string, startMs, endMs int64, extra map[string]interface{}) (map[string]interface{}, *dslErr) {
	q, derr := compileQuery(kql, schema)
	if derr != nil {
		return nil, derr
	}
	inner := map[string]interface{}{}
	if q != nil {
		inner["must"] = []interface{}{q}
	}
	inner["filter"] = []interface{}{
		map[string]interface{}{"range": map[string]interface{}{
			schema.TimeField: map[string]interface{}{"gte": startMs, "lt": endMs}}},
	}
	body := map[string]interface{}{
		"query":            map[string]interface{}{"bool": inner},
		"size":             0,
		"track_total_hits": true,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body, nil
}

// executeAnalyze 发送聚合请求并整形响应（含 total 与 sum_other_doc_count 透传）。
func executeAnalyze(c *gin.Context, client *escClient, pattern string, body map[string]interface{}) {
	status, rbody, err := client.do(c.Request.Context(), http.MethodPost,
		"/"+pattern+"/_search", mustJSON(body))
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "分析请求失败: "+err.Error())
		return
	}
	if status != http.StatusOK {
		resp.Fail(c, resp.CodeInternal, esErr(status, rbody, "分析失败").Error())
		return
	}
	var payload struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
		} `json:"hits"`
		Aggregations map[string]json.RawMessage `json:"aggregations"`
	}
	if err := json.Unmarshal(rbody, &payload); err != nil {
		resp.Fail(c, resp.CodeInternal, "解析分析响应失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"total": payload.Hits.Total.Value, "aggs": payload.Aggregations})
}

// bindJSON 统一请求体解析（分析类）。
func bindJSON(c *gin.Context, out interface{}) error {
	if err := c.ShouldBindJSON(out); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return err
	}
	return nil
}
