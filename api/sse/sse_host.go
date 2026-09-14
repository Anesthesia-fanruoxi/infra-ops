package sse

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"infra-ops/api/shared"
	"infra-ops/common/eventbus"
	"infra-ops/model"

	"github.com/gin-gonic/gin"
)

// HostStatus 保留原有主机状态 SSE 接口，兼容已有订阅方。
func (h *Handler) HostStatus(c *gin.Context) {
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
	page, pageSize := shared.ParsePage(c)
	sortBy := c.Query("sort")
	if sortBy != "" && sortBy != "name" && sortBy != "ip" {
		sortBy = "name"
	}
	order := c.Query("order")
	if order != "desc" {
		order = "asc"
	}

	c.SSEvent("connected", time.Now().Format(time.RFC3339))
	h.emitHostsPage(c, tag, status, name, ip, sortBy, order, page, pageSize)
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

func (h *Handler) emitHostsPage(c *gin.Context, tag, status, name, ip, sortBy, order string, page, pageSize int) {
	if h.hostRepo == nil {
		return
	}
	items, total, err := h.hostRepo.List(tag, status, name, ip, sortBy, order, page, pageSize)
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
