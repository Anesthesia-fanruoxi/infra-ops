package shared

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
)

// SSESetup 公共 SSE 响应头设置，返回 flusher；客户端不支持流式时返回 ok=false。
func SSESetup(c *gin.Context) (http.Flusher, bool) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		resp.ErrHTTP(c, http.StatusInternalServerError, resp.CodeInternal, "streaming not supported")
		return nil, false
	}
	return flusher, true
}
