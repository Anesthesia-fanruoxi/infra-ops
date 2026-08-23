package api

import (
	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/store"
)

type miscHandler struct {
	hostRepo  *store.HostRepo
	auditRepo *store.AuditRepo
}

func NewMiscHandler(hostRepo *store.HostRepo, auditRepo *store.AuditRepo) *miscHandler {
	return &miscHandler{hostRepo: hostRepo, auditRepo: auditRepo}
}

// Overview GET /api/overview：总览仪表盘数据。
func (h *miscHandler) Overview(c *gin.Context) {
	total, online, offline, unverified, err := h.hostRepo.CountAll()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询统计失败")
		return
	}

	byTag, err := h.hostRepo.CountByTag()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询分类统计失败")
		return
	}

	recent, err := h.auditRepo.Recent(10)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询审计日志失败")
		return
	}

	resp.OK(c, gin.H{
		"total":         total,
		"online":        online,
		"offline":       offline,
		"unverified":    unverified,
		"by_tag":        byTag,
		"recent_audits": recent,
	})
}
