// 检索 / 内存分页 / KQL 校验三个 handler（B8）。
// 响应体不含任何编译产物（§14.4）：DSL 只按 debug 级别进服务端日志（带 request_id）。
package es

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// searchReq POST /es/:id/search 请求体。start/end 为唯一时间接受体（字符串，§9）。
type searchReq struct {
	ViewID            int64    `json:"view_id" binding:"required"`
	KQL               string   `json:"kql"`
	Start             string   `json:"start" binding:"required"`
	End               string   `json:"end" binding:"required"`
	Columns           []string `json:"columns"`
	HistogramInterval string   `json:"histogram_interval"`
}

// newRequestID 短请求 ID，用于串联 debug 日志。
func newRequestID() string { return fmt.Sprintf("%x", time.Now().UnixNano()) }

// mustJSON 序列化；失败返回 null 字面量（仅用于日志与请求体）。
func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// loadSchema 读视图 + 字段表快照构建 Schema。
func (h *Handler) loadSchema(connID, viewID int64) (*model.ESView, *Schema, *dslErr) {
	if h.viewRepo == nil {
		return nil, nil, &dslErr{code: resp.CodeInternal, msg: "视图模块未初始化"}
	}
	v, err := h.viewRepo.GetByID(viewID)
	if err != nil || v == nil || v.ConnID != connID {
		return nil, nil, &dslErr{code: esCodeViewNotExist, msg: "视图不存在"}
	}
	fields := []model.ESViewField{}
	if v.FieldsJSON != "" {
		if e := json.Unmarshal([]byte(v.FieldsJSON), &fields); e != nil {
			return nil, nil, &dslErr{code: resp.CodeInternal, msg: "字段表快照损坏，请刷新视图字段"}
		}
	}
	if len(fields) == 0 {
		return nil, nil, &dslErr{code: esCodeNoTimeField, msg: "视图字段表为空，请先刷新视图字段"}
	}
	return v, BuildSchema(v, fields), nil
}

// SearchKQL 检索：一次拉满 esResultWindow 条存入内存缓存，返回 result_id（§11）。
func (h *Handler) SearchKQL(c *gin.Context) {
	connID := shared.ParseID(c)
	client, code, msg := h.resolve(connID)
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "读取请求体失败: "+err.Error())
		return
	}
	var req searchReq
	if err := json.Unmarshal(raw, &req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.ViewID == 0 || req.Start == "" || req.End == "" {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: view_id / start / end 必填")
		return
	}
	// 旧参数（JSON query DSL）显式拒绝（§14.4）
	var legacy map[string]interface{}
	if json.Unmarshal(raw, &legacy) == nil {
		if _, has := legacy["query"]; has {
			resp.Fail(c, resp.CodeBadRequest, "参数已变更：JSON query 不再接受，请改用 kql")
			return
		}
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
	body, derr := BuildSearchBody(schema, req.KQL, startMs, endMs, req.Columns, req.HistogramInterval)
	if derr != nil {
		failDSLErr(c, derr)
		return
	}
	reqID := newRequestID()
	log.Printf("[es:debug][%s] conn=%d view=%d kql=%q start=%d end=%d", reqID, connID, req.ViewID, req.KQL, startMs, endMs)
	status, rbody, err := client.do(c.Request.Context(), http.MethodPost,
		"/"+view.IndexPattern+"/_search", mustJSON(body))
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "检索请求失败: "+err.Error())
		return
	}
	if status != http.StatusOK {
		resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, esErr(status, rbody, "检索失败").Error())
		return
	}
	rs, perr := parseSearchResponse(reqID, connID, req.ViewID, schema, req, startMs, endMs, rbody)
	if perr != nil {
		resp.Fail(c, resp.CodeInternal, perr.Error())
		return
	}
	cachePut(rs)
	log.Printf("[es:debug][%s] dsl=%s", reqID, string(mustJSON(body)))
	resp.OK(c, gin.H{
		"result_id": rs.ResultID, "total": rs.Total,
		"window": esResultWindow, "window_truncated": rs.Total > esResultWindow,
		"view_id": rs.ViewID, "index_pattern": schema.Pattern, "time_field": schema.TimeField,
		"start": req.Start, "start_ms": startMs, "end": req.End, "end_ms": endMs,
		"columns": req.Columns, "hits": rs.Hits, "histogram": rs.Histogram,
	})
}

// parseSearchResponse 整形 ES 响应为内存结果集。
func parseSearchResponse(reqID string, connID, viewID int64, schema *Schema,
	req searchReq, startMs, endMs int64, body []byte) (*resultSet, error) {
	var payload struct {
		Took     int64 `json:"took"`
		TimedOut bool  `json:"timed_out"`
		Hits     struct {
			Total struct {
				Value    int64  `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Index     string                 `json:"_index"`
				ID        string                 `json:"_id"`
				Score     interface{}            `json:"_score"`
				Source    map[string]interface{} `json:"_source"`
				Highlight map[string]interface{} `json:"highlight"`
			} `json:"hits"`
		} `json:"hits"`
		Aggregations map[string]struct {
			Buckets []histBucket `json:"buckets"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析检索响应失败: %v", err)
	}
	log.Printf("[es:debug][%s] took=%dms total=%d rel=%s timed_out=%v",
		reqID, payload.Took, payload.Hits.Total.Value, payload.Hits.Total.Relation, payload.TimedOut)
	rs := &resultSet{
		ConnID: connID, ViewID: viewID, Total: payload.Hits.Total.Value,
		TimeField: schema.TimeField, StartMS: startMs, EndMS: endMs,
		Columns: req.Columns, Hits: make([]cachedHit, 0, len(payload.Hits.Hits)),
	}
	for _, hh := range payload.Hits.Hits {
		rs.Hits = append(rs.Hits, cachedHit{
			Index: hh.Index, ID: hh.ID, Score: hh.Score, Source: hh.Source, Highlight: hh.Highlight,
		})
	}
	if agg, ok := payload.Aggregations["histogram"]; ok {
		rs.Histogram = agg.Buckets
	}
	return rs, nil
}

// SearchPage 内存分页：不访问 ES；result_id 不匹配 → 4011，越界 → 4005（§14.5）。
func (h *Handler) SearchPage(c *gin.Context) {
	connID := shared.ParseID(c)
	var req struct {
		ResultID string `json:"result_id" binding:"required"`
		From     int    `json:"from"`
		Size     int    `json:"size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	hits, total, derr := cachePage(connID, parseViewFromResultID(req.ResultID), req.ResultID, req.From, req.Size)
	if derr != nil {
		if derr.code == esCodeResultStale {
			resp.ErrHTTP(c, http.StatusConflict, derr.code, derr.msg)
			return
		}
		resp.ErrHTTP(c, http.StatusBadRequest, derr.code, derr.msg)
		return
	}
	resp.OK(c, gin.H{"result_id": req.ResultID, "total": total,
		"from": req.From, "size": len(hits), "hits": hits})
}

// parseViewFromResultID result_id 形如 "conn-view-seq"。
func parseViewFromResultID(id string) int64 {
	var conn, view int64
	if _, err := fmt.Sscanf(id, "%d-%d", &conn, &view); err != nil {
		return 0
	}
	return view
}

// ValidateKQL 只校验不执行：恒返回 200，失败时带 code/pos/len（§14.4）。
func (h *Handler) ValidateKQL(c *gin.Context) {
	connID := shared.ParseID(c)
	var req struct {
		ViewID int64  `json:"view_id" binding:"required"`
		KQL    string `json:"kql"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	_, schema, derr := h.loadSchema(connID, req.ViewID)
	if derr != nil {
		resp.OK(c, gin.H{"ok": false, "error": gin.H{
			"code": derr.code, "message": derr.msg, "pos": derr.pos, "len": derr.len}})
		return
	}
	if _, derr := compileQuery(req.KQL, schema); derr != nil {
		resp.OK(c, gin.H{"ok": false, "error": gin.H{
			"code": derr.code, "message": derr.msg, "pos": derr.pos, "len": derr.len}})
		return
	}
	resp.OK(c, gin.H{"ok": true})
}

// failDSLErr KQL 编译错误 → 400 + 业务码。
func failDSLErr(c *gin.Context, derr *dslErr) {
	resp.ErrHTTP(c, http.StatusBadRequest, derr.code, derr.msg)
}
