package sse

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"infra-ops/common/eventbus"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	bus       *eventbus.Bus
	hostRepo  *repo.HostRepo
	credRepo  *repo.CredentialRepo
	auditRepo *repo.AuditRepo
}

func NewHandler(bus *eventbus.Bus, hostRepo *repo.HostRepo, credRepo *repo.CredentialRepo, auditRepo *repo.AuditRepo) *Handler {
	return &Handler{bus: bus, hostRepo: hostRepo, credRepo: credRepo, auditRepo: auditRepo}
}

// Overview SSE 总览流：按主机汇总、主机速览、操作日志三个板块分别推送完整快照。
func (h *Handler) Overview(c *gin.Context) {
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

func (h *Handler) subscribeOverview(sections chan<- string) []func() {
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

func (h *Handler) prepareStream(c *gin.Context) bool {
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

func (h *Handler) emitHostSummary(c *gin.Context) {
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

func (h *Handler) emitHostOverview(c *gin.Context) {
	if h.hostRepo == nil {
		return
	}
	items, total, err := h.hostRepo.List("", "", "", "", "name", "asc", 1, 6)
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

func (h *Handler) emitOperationLogs(c *gin.Context) {
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

func (h *Handler) sendJSONEvent(c *gin.Context, name string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	c.SSEvent(name, string(payload))
}
