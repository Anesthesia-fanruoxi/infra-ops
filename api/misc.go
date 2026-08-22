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

// Overview GET /api/v1/overview
func (h *miscHandler) Overview(c *gin.Context) {
	total, online, offline, unverified, err := h.hostRepo.CountAll()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "鏌ヨ缁熻澶辫触")
		return
	}

	byTag, err := h.hostRepo.CountByTag()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "鏌ヨ瑙掕壊缁熻澶辫触")
		return
	}

	recent, err := h.auditRepo.Recent(10)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "鏌ヨ瀹¤鏃ュ織澶辫触")
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

// AuditLogs GET /api/v1/audit-logs
func (h *miscHandler) AuditLogs(c *gin.Context) {
	action := c.Query("action")
	page, pageSize := parsePage(c)

	items, total, err := h.auditRepo.List(action, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "鏌ヨ瀹¤鏃ュ織澶辫触")
		return
	}

	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}
