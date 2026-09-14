package sse

import (
	"io"
	"net/http"
	"strings"
	"time"

	"infra-ops/api/shared"
	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"

	"github.com/gin-gonic/gin"
)

// Audits GET /api/sse/audits：审计日志统一查询流。
// 筛选/分页参数经 URL 传入；建连即推统计与日志快照，
// 之后新日志匹配当前筛选时增量推送，统计随新日志刷新。
func (h *Handler) Audits(c *gin.Context) {
	if !h.prepareStream(c) {
		return
	}
	q := repo.AuditQuery{
		Action:  c.Query("action"),
		Status:  c.Query("status"),
		Keyword: c.Query("keyword"),
		From:    c.Query("from"),
		To:      c.Query("to"),
	}
	q.Page, q.PageSize = shared.ParsePage(c)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, "streaming not supported")
		return
	}

	emitSnapshot := func() {
		if stats, err := h.auditRepo.Stats(); err == nil {
			h.sendJSONEvent(c, "stats", stats)
		}
		items, total, err := h.auditRepo.List(q)
		if err != nil {
			return
		}
		if items == nil {
			items = []model.AuditLog{}
		}
		h.sendJSONEvent(c, "logs", resp.PageData{List: items, Total: total, Page: q.Page, PageSize: q.PageSize})
	}

	ch := make(chan eventbus.Event, 64)
	var subID int
	if h.bus != nil {
		subID = h.bus.Subscribe(eventbus.TopicAuditCreated, func(ev eventbus.Event) {
			select {
			case ch <- ev:
			default:
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicAuditCreated, subID)
	}

	c.SSEvent("connected", time.Now().Format(time.RFC3339))
	emitSnapshot()
	flusher.Flush()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case ev := <-ch:
			if entry, ok := ev.Data.(model.AuditLog); ok && matchAuditQuery(entry, q) {
				h.sendJSONEvent(c, "append", entry)
			}
			if stats, err := h.auditRepo.Stats(); err == nil {
				h.sendJSONEvent(c, "stats", stats)
			}
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		case <-c.Request.Context().Done():
			return false
		}
		flusher.Flush()
		return true
	})
}

// matchAuditQuery 内存侧筛选判定，与 store 层 SQL 条件保持一致。
func matchAuditQuery(a model.AuditLog, q repo.AuditQuery) bool {
	if q.Action != "" && !strings.HasPrefix(a.Action, q.Action) {
		return false
	}
	switch q.Status {
	case "fail":
		if !strings.HasSuffix(a.Action, "_fail") {
			return false
		}
	case "success":
		if strings.HasSuffix(a.Action, "_fail") {
			return false
		}
	}
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		if !strings.Contains(a.Detail, kw) && !strings.Contains(a.RemoteIP, kw) {
			return false
		}
	}
	if q.From != "" && a.CreatedAt < q.From {
		return false
	}
	if q.To != "" && a.CreatedAt > q.To {
		return false
	}
	return true
}
