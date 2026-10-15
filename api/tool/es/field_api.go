// field_api.go 「系统自带字段」缓存的三个接口：读、存人工补充、立即重探。
//
// 为什么 GET 不顺手做「没有就现探」：探测要打集群（版本 + 组件模板 + field_caps 三个请求），
// 而这条路会被反复调用；让一个查询接口夹带重活，调用方就得替它的超时负责。
// 没探测过时直接回 cache_status=""，界面显示「尚未探测，点重新探测」就够了。
package es

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
	"infra-ops/model"
)

// esFieldExtraMax 人工补充清单的条数上限（够登记几个采集器自定义字段了，再多说明该回头看探测）。
const esFieldExtraMax = 40

// FieldCacheGet GET /api/es/:id/default-fields
func (h *Handler) FieldCacheGet(c *gin.Context) {
	connID := shared.ParseID(c)
	if conn, err := h.repo.GetByID(connID); err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	if h.fieldRepo == nil {
		resp.Fail(c, resp.CodeInternal, "字段缓存未启用")
		return
	}
	cache, err := h.fieldRepo.Get(connID)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取字段缓存失败: "+err.Error())
		return
	}
	resp.OK(c, fieldCachePayload(connID, cache))
}

// FieldCachePutExtra PUT /api/es/:id/default-fields —— 只改人工补充清单，不碰探测结果。
func (h *Handler) FieldCachePutExtra(c *gin.Context) {
	connID := shared.ParseID(c)
	if conn, err := h.repo.GetByID(connID); err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	if h.fieldRepo == nil {
		resp.Fail(c, resp.CodeInternal, "字段缓存未启用")
		return
	}
	var req struct {
		Extra []string `json:"extra"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	// 字段名会被原样渲染进配置文本，非法字符先拒（与 tag / source 同一套理由：
	// 坏配置不报错、只在 logstash 加载时才炸，查起来费劲）
	clean := make([]string, 0, len(req.Extra))
	seen := map[string]bool{}
	for _, f := range req.Extra {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		if strings.ContainsAny(f, " \"'\\\n\r\t,[]{}") {
			resp.Fail(c, esCodeGrokInvalid, "字段名 "+f+" 含非法字符（只允许字母数字与 _ - . @ 这类）")
			return
		}
		seen[f] = true
		clean = append(clean, f)
		if len(clean) >= esFieldExtraMax {
			break
		}
	}
	if err := h.fieldRepo.SaveExtra(connID, clean); err != nil {
		resp.Fail(c, resp.CodeInternal, "保存失败: "+err.Error())
		return
	}
	cache, err := h.fieldRepo.Get(connID)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取字段缓存失败: "+err.Error())
		return
	}
	resp.OK(c, fieldCachePayload(connID, cache))
}

// FieldCacheRefresh POST /api/es/:id/default-fields/refresh
//
// 同步探测一次再返回：这是用户点「重新探测」触发的动作，丢后台再让他自己去刷新页面
// 反而看不出发生了什么。前端对这个请求要关掉 axios 超时（探测最长 20s），
// 与导出 / 导入 / 备份那些长耗时接口同一套做法。
func (h *Handler) FieldCacheRefresh(c *gin.Context) {
	connID := shared.ParseID(c)
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "ES 连接不存在")
		return
	}
	if h.fieldRepo == nil {
		resp.Fail(c, resp.CodeInternal, "字段缓存未启用")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), esFieldProbeTimeout)
	defer cancel()
	// 探测失败**不在这里报错**：probeAndSave 已经把 failed + 原因落到缓存里了，
	// 统一从缓存回给前端，界面只有一条渲染路径（status + error）。
	_ = probeAndSave(ctx, h.cryptoS, h.fieldRepo, conn)

	cache, err := h.fieldRepo.Get(connID)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取字段缓存失败: "+err.Error())
		return
	}
	resp.OK(c, fieldCachePayload(connID, cache))
}

// fieldCachePayload 统一响应形状：没有记录时也给出结构完整的空壳 + 候选池，
// 前端不必为「有没有缓存记录」写两套分支。
func fieldCachePayload(connID int64, c *model.ESFieldCache) gin.H {
	out := gin.H{
		"conn_id":    connID,
		"es_version": "", "fields": []string{}, "extra": []string{},
		"status": "", "error": "", "synced_at": "", "updated_at": "",
		// candidates 给界面提示「自动发现只会覆盖这些元数据对象」，
		// 剩下的（offset / logYear 之类）去人工补充里填
		"candidates": esMetaCandidateTop,
	}
	if c == nil {
		return out
	}
	out["es_version"] = c.ESVersion
	out["fields"] = c.Fields
	out["extra"] = c.Extra
	out["status"] = c.Status
	out["error"] = c.Error
	out["synced_at"] = c.SyncedAt
	out["updated_at"] = c.UpdatedAt
	return out
}
