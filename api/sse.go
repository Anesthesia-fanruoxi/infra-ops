package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"

	"github.com/gin-gonic/gin"
)

type sseHandler struct {
	bus       *eventbus.Bus
	hostRepo  *store.HostRepo
	credRepo  *store.CredentialRepo
	auditRepo *store.AuditRepo
}

func NewSSEHandler(bus *eventbus.Bus, hostRepo *store.HostRepo, credRepo *store.CredentialRepo, auditRepo *store.AuditRepo) *sseHandler {
	return &sseHandler{bus: bus, hostRepo: hostRepo, credRepo: credRepo, auditRepo: auditRepo}
}

// Overview SSE 总览流：按主机汇总、主机速览、操作日志三个板块分别推送完整快照。
func (h *sseHandler) Overview(c *gin.Context) {
	if !h.prepareStream(c) {
		return
	}

	sections := make(chan string, 32)
	unsubscribers := h.subscribeOverview(sections)
	defer func() {
		for _, unsubscribe := range unsubscribers {
			unsubscribe()
		}
	}()

	c.SSEvent("connected", time.Now().Format(time.RFC3339))
	h.emitHostSummary(c)
	h.emitHostOverview(c)
	h.emitOperationLogs(c)
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case section := <-sections:
			switch section {
			case "host.summary":
				h.emitHostSummary(c)
			case "host.overview":
				h.emitHostOverview(c)
			case "operation.logs":
				h.emitOperationLogs(c)
			}
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return true
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

func (h *sseHandler) subscribeOverview(sections chan<- string) []func() {
	if h.bus == nil {
		return nil
	}

	var unsubscribers []func()
	subscribe := func(topic string, names ...string) {
		id := h.bus.Subscribe(topic, func(eventbus.Event) {
			for _, name := range names {
				select {
				case sections <- name:
				default:
					// Coalesce refresh notifications when the client is slow.
				}
			}
		})
		unsubscribers = append(unsubscribers, func() { h.bus.Unsubscribe(topic, id) })
	}

	subscribe(eventbus.TopicHostStatus, "host.summary", "host.overview")
	subscribe(eventbus.TopicHostChanged, "host.summary", "host.overview")
	subscribe(eventbus.TopicCredentialChanged, "host.summary")
	subscribe(eventbus.TopicAuditCreated, "operation.logs")
	return unsubscribers
}

func (h *sseHandler) prepareStream(c *gin.Context) bool {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if _, ok := c.Writer.(http.Flusher); !ok {
		resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, "streaming response not supported")
		return false
	}
	return true
}

func (h *sseHandler) emitHostSummary(c *gin.Context) {
	if h.hostRepo == nil {
		return
	}
	total, online, offline, unverified, err := h.hostRepo.CountAll()
	if err != nil {
		return
	}
	byTag, err := h.hostRepo.CountByTag()
	if err != nil {
		return
	}
	credTotal := int64(0)
	if h.credRepo != nil {
		credTotal, _ = h.credRepo.Count()
	}

	onlineRate := 0
	if total > 0 {
		onlineRate = int(online * 100 / total)
	}
	h.sendJSONEvent(c, "host.summary", gin.H{
		"total":            total,
		"online":           online,
		"offline":          offline,
		"unverified":       unverified,
		"online_rate":      onlineRate,
		"credential_total": credTotal,
		"by_tag":           byTag,
	})
}

func (h *sseHandler) emitHostOverview(c *gin.Context) {
	if h.hostRepo == nil {
		return
	}
	items, total, err := h.hostRepo.List("", "", "", "", 1, 6)
	if err != nil {
		return
	}
	if items == nil {
		items = []model.Host{}
	}
	h.sendJSONEvent(c, "host.overview", gin.H{
		"list":      items,
		"total":     total,
		"page":      1,
		"page_size": 6,
	})
}

func (h *sseHandler) emitOperationLogs(c *gin.Context) {
	if h.auditRepo == nil {
		return
	}
	items, err := h.auditRepo.Recent(10)
	if err != nil {
		return
	}
	if items == nil {
		items = []model.AuditLog{}
	}
	h.sendJSONEvent(c, "operation.logs", gin.H{"list": items})
}

func (h *sseHandler) sendJSONEvent(c *gin.Context, name string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	c.SSEvent(name, string(payload))
}

// HostStatus 保留原有主机状态 SSE 接口，兼容已有订阅方。
func (h *sseHandler) HostStatus(c *gin.Context) {
	if !h.prepareStream(c) {
		return
	}

	ch := make(chan eventbus.Event, 64)
	var subID int
	if h.bus != nil {
		subID = h.bus.Subscribe(eventbus.TopicHostStatus, func(ev eventbus.Event) {
			select {
			case ch <- ev:
			default:
			}
		})
		defer h.bus.Unsubscribe(eventbus.TopicHostStatus, subID)
	}

	tag := strings.TrimSpace(c.Query("tag"))
	if tag == "" {
		tag = strings.TrimSpace(c.Query("role")) // deprecated query alias
	}
	status := c.Query("status")
	name := c.Query("name")
	ip := c.Query("ip")
	if keyword := c.Query("keyword"); keyword != "" && name == "" && ip == "" {
		name, ip = keyword, keyword
	}
	page, pageSize := parsePage(c)

	c.SSEvent("connected", time.Now().Format(time.RFC3339))
	h.emitHostsPage(c, tag, status, name, ip, page, pageSize)
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	c.Stream(func(w io.Writer) bool {
		select {
		case ev := <-ch:
			data, _ := json.Marshal(ev.Data)
			c.SSEvent(eventbus.TopicHostStatus, string(data))
			return true
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

// Audits GET /api/sse/audits：审计日志统一查询流。
// 筛选/分页参数经 URL 传入；建连即推统计与日志快照，
// 之后新日志匹配当前筛选时增量推送，统计随新日志刷新。
func (h *sseHandler) Audits(c *gin.Context) {
	if !h.prepareStream(c) {
		return
	}
	q := store.AuditQuery{
		Action:  c.Query("action"),
		Status:  c.Query("status"),
		Keyword: c.Query("keyword"),
		From:    c.Query("from"),
		To:      c.Query("to"),
	}
	q.Page, q.PageSize = parsePage(c)

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
func matchAuditQuery(a model.AuditLog, q store.AuditQuery) bool {
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

func (h *sseHandler) emitHostsPage(c *gin.Context, tag, status, name, ip string, page, pageSize int) {
	if h.hostRepo == nil {
		return
	}
	items, total, err := h.hostRepo.List(tag, status, name, ip, page, pageSize)
	if err != nil {
		return
	}
	if items == nil {
		items = []model.Host{}
	}
	h.sendJSONEvent(c, "hosts", gin.H{
		"list":      items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
