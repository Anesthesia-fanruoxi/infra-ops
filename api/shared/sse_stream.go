package shared

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SSEStream 接口层的流式写出封装：建流 → 逐帧发送 → 立即冲刷。
//
// AI 生成类接口用它把「增量草稿 / 状态切换 / 终稿」推给前端；帧序即语义，
// 故每帧都冲刷，不做任何攒批（攒批由上游按需做）。
type SSEStream struct {
	c       *gin.Context
	flusher http.Flusher
}

// NewSSEStream 设置 SSE 响应头并返回写出器；客户端不支持流式时写错误响应并返回 ok=false。
// 注意：调用后响应头已经发出，业务错误只能以事件帧（如 event: error）回传，不能再走 resp.Fail。
func NewSSEStream(c *gin.Context) (*SSEStream, bool) {
	flusher, ok := SSESetup(c)
	if !ok {
		return nil, false
	}
	return &SSEStream{c: c, flusher: flusher}, true
}

// Send 发送一帧（event + JSON data）并立即冲刷。
func (s *SSEStream) Send(event string, data any) {
	s.c.SSEvent(event, data)
	s.flusher.Flush()
}
