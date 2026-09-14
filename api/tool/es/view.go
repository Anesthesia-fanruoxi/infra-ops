// 数据视图 CRUD 与探测：六端点 + probe。
package es

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

// statsSnapshot 视图命中概览快照（存 stats_json）。
type statsSnapshot struct {
	Indices   []string `json:"indices"`
	Aliases   []string `json:"aliases"`
	Streams   []string `json:"streams"`
	DocsCount int64    `json:"docs_count"`
	Truncated bool     `json:"truncated"`
}

// viewUpsertReq 创建/更新视图请求体。time_field 为空时由后端自动从候选挑选。
type viewUpsertReq struct {
	Name         string `json:"name" binding:"required"`
	IndexPattern string `json:"index_pattern" binding:"required"`
	TimeField    string `json:"time_field"`
}

// Probe 探测 index pattern（创建向导用），不落库。
func (h *Handler) Probe(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		IndexPattern string `json:"index_pattern" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	cc, err := collateView(c.Request.Context(), client, req.IndexPattern)
	if err != nil {
		h.failViewErr(c, err)
		return
	}
	resp.OK(c, gin.H{
		"indices": cc.Indices, "aliases": cc.Aliases, "data_streams": cc.Streams,
		"docs_count":  cc.DocsCount,
		"time_fields": cc.TimeCandidates,
		"fields":      cc.Fields, "field_count": len(cc.Fields), "truncated": cc.Truncated,
	})
}

// ListViews 视图列表。
func (h *Handler) ListViews(c *gin.Context) {
	if h.viewRepo == nil {
		resp.Fail(c, resp.CodeInternal, "视图模块未初始化")
		return
	}
	connID := shared.ParseID(c)
	list, err := h.viewRepo.ListByConn(connID)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "查询视图失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, v := range list {
		out = append(out, viewListItem(v))
	}
	resp.OK(c, out)
}

func viewListItem(v model.ESView) gin.H {
	nf := int64(0)
	if v.FieldsJSON != "" {
		var f []model.ESViewField
		if json.Unmarshal([]byte(v.FieldsJSON), &f) == nil {
			nf = int64(len(f))
		}
	}
	return gin.H{
		"id": v.ID, "name": v.Name, "index_pattern": v.IndexPattern,
		"time_field": v.TimeField, "field_count": nf,
		"sync_status": v.SyncStatus, "sync_error": v.SyncError, "synced_at": v.SyncedAt,
		"created_at": v.CreatedAt, "updated_at": v.UpdatedAt,
	}
}

// CreateView 创建视图；创建时同步探测一次并落库字段表与命中概览。
func (h *Handler) CreateView(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req viewUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	cc, err := collateView(c.Request.Context(), client, req.IndexPattern)
	if err != nil {
		h.failViewErr(c, err)
		return
	}
	timeField, verr := resolveTimeField(cc, req.TimeField)
	if verr != nil {
		resp.Fail(c, verr.code, verr.msg)
		return
	}
	id, err := h.viewRepo.Create(&model.ESView{
		ConnID: shared.ParseID(c), Name: req.Name,
		IndexPattern: req.IndexPattern, TimeField: timeField,
	})
	if err != nil {
		if shared.IsDuplicateErr(err) {
			resp.Fail(c, esCodeViewNameDup, "视图名称在同一连接内已存在")
			return
		}
		resp.Fail(c, resp.CodeInternal, "创建视图失败: "+err.Error())
		return
	}
	// 创建即同步：写首次字段表快照。
	h.applySnapshot(id, cc)
	resp.OK(c, gin.H{"id": id})
}

// GetView 视图详情（含完整字段表）。
func (h *Handler) GetView(c *gin.Context) {
	connID := shared.ParseID(c)
	id := shared.ParseIDParam(c, "vid")
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的视图 ID")
		return
	}
	v, err := h.viewRepo.GetByID(id)
	if err != nil || v == nil || v.ConnID != connID {
		resp.Fail(c, esCodeViewNotExist, "视图不存在")
		return
	}
	fields := []model.ESViewField{}
	if v.FieldsJSON != "" {
		json.Unmarshal([]byte(v.FieldsJSON), &fields)
	}
	resp.OK(c, gin.H{
		"id": v.ID, "name": v.Name, "index_pattern": v.IndexPattern,
		"time_field": v.TimeField, "fields": fields,
		"sync_status": v.SyncStatus, "sync_error": v.SyncError, "synced_at": v.SyncedAt,
		"created_at": v.CreatedAt, "updated_at": v.UpdatedAt,
	})
}

// UpdateView 更新视图；改 pattern 或时间字段后自动重同步。
func (h *Handler) UpdateView(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	id := shared.ParseIDParam(c, "vid")
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的视图 ID")
		return
	}
	cur, err := h.viewRepo.GetByID(id)
	if err != nil || cur == nil {
		resp.Fail(c, esCodeViewNotExist, "视图不存在")
		return
	}
	var req viewUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	// 改 pattern 或时间字段 → 需要重新探测；仅改名则不动字段表。
	var cc *collateResult
	if req.IndexPattern != "" && req.IndexPattern != cur.IndexPattern || req.TimeField != "" && req.TimeField != cur.TimeField {
		cc, err = collateView(c.Request.Context(), client, req.IndexPattern)
		if err != nil {
			h.failViewErr(c, err)
			return
		}
	}
	var timeField string
	if req.TimeField != "" {
		if cc == nil {
			resp.Fail(c, resp.CodeBadRequest, "时间字段变更需同时提供 index_pattern")
			return
		}
		tf, verr := resolveTimeField(cc, req.TimeField)
		if verr != nil {
			resp.Fail(c, verr.code, verr.msg)
			return
		}
		timeField = tf
	} else if cc != nil {
		tf, verr := resolveTimeField(cc, cur.TimeField)
		if verr != nil {
			resp.Fail(c, verr.code, verr.msg)
			return
		}
		timeField = tf
	} else {
		timeField = cur.TimeField
	}
	if req.Name == "" {
		req.Name = cur.Name
	}
	if err := h.viewRepo.UpdateBasic(id, req.Name, req.IndexPattern, timeField); err != nil {
		if shared.IsDuplicateErr(err) {
			resp.Fail(c, esCodeViewNameDup, "视图名称在同一连接内已存在")
			return
		}
		resp.Fail(c, resp.CodeInternal, "更新视图失败: "+err.Error())
		return
	}
	if cc != nil {
		h.applySnapshot(id, cc)
	}
	resp.OK(c, nil)
}

// DeleteView 删除视图（不触碰 ES）。
func (h *Handler) DeleteView(c *gin.Context) {
	id := shared.ParseIDParam(c, "vid")
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的视图 ID")
		return
	}
	if err := h.viewRepo.Delete(id); err != nil {
		resp.Fail(c, resp.CodeInternal, "删除视图失败: "+err.Error())
		return
	}
	resp.OK(c, nil)
}

// RefreshView 手动刷新单个视图：同步执行，返回新字段表。
func (h *Handler) RefreshView(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	id := shared.ParseIDParam(c, "vid")
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的视图 ID")
		return
	}
	v, err := h.viewRepo.GetByID(id)
	if err != nil || v == nil {
		resp.Fail(c, esCodeViewNotExist, "视图不存在")
		return
	}
	cc, err := collateView(c.Request.Context(), client, v.IndexPattern)
	if err != nil {
		h.failViewErr(c, err)
		return
	}
	timeField, verr := resolveTimeField(cc, v.TimeField)
	if verr != nil {
		resp.Fail(c, verr.code, verr.msg)
		return
	}
	if timeField != v.TimeField {
		h.viewRepo.UpdateBasic(id, v.Name, v.IndexPattern, timeField)
	}
	h.applySnapshot(id, cc)
	resp.OK(c, gin.H{"time_field": timeField, "fields": cc.Fields, "truncated": cc.Truncated})
}

// IndexMapping 单索引原始 mapping（§14.3，排障用，不合并不落库）。
func (h *Handler) IndexMapping(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	index := c.Param("index")
	if index == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少索引名")
		return
	}
	m, err := fetchSingleMapping(c.Request.Context(), client, index)
	if err != nil {
		if err.Error() == "索引不存在" {
			resp.Fail(c, esCodeViewNotExist, err.Error())
			return
		}
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, gin.H{"index": index, "mapping": m})
}

// resolveTimeField 从合并结果解析时间字段：空则取候选第一个；
// 指定但非 date / 部分成员缺失 / 多类型冲突 → 4009；无任何候选 → 4004。
func resolveTimeField(cc *collateResult, want string) (string, *esViewErr) {
	if want != "" {
		for i := range cc.Fields {
			f := &cc.Fields[i]
			if f.Path != want {
				continue
			}
			if f.TypeConflict {
				return "", &esViewErr{code: esCodeTimeFieldConflict, msg: "时间字段在部分成员中类型不一致"}
			}
			if f.Partial {
				return "", &esViewErr{code: esCodeTimeFieldConflict, msg: "时间字段在部分成员中缺失"}
			}
			if !containsDateType(f.Types) {
				return "", &esViewErr{code: esCodeTimeFieldConflict, msg: "指定的时间字段不是 date 类型"}
			}
			return want, nil
		}
		return "", &esViewErr{code: esCodeTimeFieldConflict, msg: "指定的时间字段不存在"}
	}
	if len(cc.TimeCandidates) == 0 {
		return "", &esViewErr{code: esCodeNoTimeField, msg: "未探测到可用时间字段"}
	}
	return cc.TimeCandidates[0], nil
}

// applySnapshot 把一次探测结果写为字段表快照与命中概览（单事务）。
func (h *Handler) applySnapshot(viewID int64, cc *collateResult) {
	if h.viewRepo == nil {
		return
	}
	if cc == nil {
		return
	}
	fieldsJSON, _ := json.Marshal(cc.Fields)
	stats := statsSnapshot{Indices: cc.Indices, Aliases: cc.Aliases, Streams: cc.Streams,
		DocsCount: cc.DocsCount, Truncated: cc.Truncated}
	statsJSON, _ := json.Marshal(stats)
	h.viewRepo.SaveSyncResults([]repo.SyncUpdate{
		{ViewID: viewID, FieldsJSON: string(fieldsJSON), StatsJSON: string(statsJSON)},
	})
}

// failViewErr 把单测探测错误映射为响应：未命中/时间字段问题走 400，其它 500。
func (h *Handler) failViewErr(c *gin.Context, err error) {
	var ve *esViewErr
	if errors.As(err, &ve) {
		if ve.code == esCodeIndexNoMatch {
			resp.Fail(c, ve.code, ve.msg)
			return
		}
		resp.Fail(c, resp.CodeInternal, ve.msg)
		return
	}
	resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, "探测失败: "+err.Error())
}
