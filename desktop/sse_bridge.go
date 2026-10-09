// 桌面模式（Wails v3）桥接层：把 Gin 引擎内的能力转接到 Wails 窗口。
//
// sse_bridge.go：SSE 事件桥。
// 背景：Wails 资产服务器对长连接流式响应支持不完整（wailsapp/wails#2847），
// 直接把 /api/sse/* 端点暴露给 WebView 会阻塞/整段缓冲。本桥在进程内直接执行
// Gin 引擎（自定义 ResponseWriter 实现 http.Flusher），把增量数据经 Wails
// 事件推送到前端。前端 wails-shim.js 覆盖 window.EventSource 后以相同 API
// 消费，现有 9 处 new EventSource(...) 调用点零修改。
package desktop

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// clientIDRe 限制订阅标识字符集，防止事件名注入（"sse:" + clientID）。
var clientIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SSEBridge 把 Gin 引擎内的 SSE 响应流转发为 Wails 事件。
// 事件名：sse:<clientID>（增量文本块）、sse:<clientID>:end（流结束，数据 {status}）。
type SSEBridge struct {
	app     *application.App
	handler http.Handler

	mu   sync.Mutex
	subs map[string]context.CancelFunc
}

// NewSSEBridge 创建桥实例；handler 为路由装配产出的 Gin 引擎。
func NewSSEBridge(app *application.App, handler http.Handler) *SSEBridge {
	return &SSEBridge{app: app, handler: handler, subs: make(map[string]context.CancelFunc)}
}

// Open 订阅一个应用内 SSE 端点。clientID 由前端生成并在调用前完成事件监听
// 注册（事件名依赖 clientID），避免连接快照帧在监听注册前丢失。
// rawURL 为应用内相对路径（如 /api/sse/hosts?tag=x）。
// 订阅成功后数据经 sse:<clientID> 事件推送。
func (b *SSEBridge) Open(clientID string, rawURL string) error {
	if !clientIDRe.MatchString(clientID) {
		return fmt.Errorf("非法的订阅标识: %q", clientID)
	}
	if !strings.HasPrefix(rawURL, "/") || strings.HasPrefix(rawURL, "//") {
		return fmt.Errorf("非法的流地址: %q", rawURL)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://desktop.internal"+rawURL, nil)
	if err != nil {
		cancel()
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	b.mu.Lock()
	if _, exists := b.subs[clientID]; exists {
		b.mu.Unlock()
		cancel()
		return fmt.Errorf("订阅标识已存在: %s", clientID)
	}
	b.subs[clientID] = cancel
	b.mu.Unlock()

	w := newBridgeWriter(b, clientID, ctx)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// gin.Recovery 通常在 handler 内生效；此处兜底保证桥自身不崩进程。
				log.Printf("[sse-bridge] 订阅 %s 执行异常: %v", clientID, r)
			}
			b.mu.Lock()
			delete(b.subs, clientID)
			b.mu.Unlock()
			cancel()
			w.finish()
			// contentType 供前端区分「SSE 流正常结束」与「HTTP 200 + 业务错误 JSON」
			// （项目 resp.Fail 为 200 + 业务码，不能仅靠状态码判断）
			b.app.Event.Emit("sse:"+clientID+":end", map[string]any{
				"status":      w.Status(),
				"contentType": w.ContentType(),
			})
		}()
		b.handler.ServeHTTP(w, req)
	}()
	return nil
}

// Close 取消指定订阅（前端 EventSource.close() 触发）；流自然结束时自动清理。
func (b *SSEBridge) Close(clientID string) {
	b.mu.Lock()
	cancel := b.subs[clientID]
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Active 返回当前活跃订阅数（诊断用）。
func (b *SSEBridge) Active() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// bridgeWriter 满足 Gin 流式响应所需的全部接口：
// http.ResponseWriter、http.Flusher（flush 触发事件推送），
// 以及 http.CloseNotifier（gin Context.Stream 直接断言，缺失会 panic）。
// 写入先累积到缓冲区，flush 时整体推送，保证 SSE 帧（event/data 行）完整成组。
type bridgeWriter struct {
	bridge   *SSEBridge
	clientID string

	header      http.Header
	status      int
	wroteHeader bool

	mu  sync.Mutex
	buf bytes.Buffer

	closeCh   chan bool
	closeOnce sync.Once
}

func newBridgeWriter(b *SSEBridge, clientID string, ctx context.Context) *bridgeWriter {
	w := &bridgeWriter{
		bridge:   b,
		clientID: clientID,
		header:   make(http.Header),
		status:   http.StatusOK,
		closeCh:  make(chan bool, 1),
	}
	go func() {
		<-ctx.Done()
		w.closeOnce.Do(func() { close(w.closeCh) })
	}()
	return w
}

func (w *bridgeWriter) Header() http.Header { return w.header }

func (w *bridgeWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
}

func (w *bridgeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.wroteHeader = true
	w.buf.Write(p)
	w.mu.Unlock()
	return len(p), nil
}

// Flush 实现 http.Flusher：推送当前缓冲的增量文本。
func (w *bridgeWriter) Flush() { w.drain() }

// CloseNotify 实现 http.CloseNotifier：ctx 取消或流结束后关闭。
func (w *bridgeWriter) CloseNotify() <-chan bool { return w.closeCh }

// Status 返回响应状态码（默认 200）。
func (w *bridgeWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// ContentType 返回响应 Content-Type（ServeHTTP 返回后读取，时序安全）。
func (w *bridgeWriter) ContentType() string { return w.header.Get("Content-Type") }

// drain 把缓冲区内的累积文本作为一个事件块推送。
func (w *bridgeWriter) drain() {
	w.mu.Lock()
	if w.buf.Len() == 0 {
		w.mu.Unlock()
		return
	}
	chunk := w.buf.String()
	w.buf.Reset()
	w.mu.Unlock()
	w.bridge.app.Event.Emit("sse:"+w.clientID, chunk)
}

// finish 流结束时冲刷残余数据并关闭 CloseNotify 通道。
func (w *bridgeWriter) finish() {
	w.drain()
	w.closeOnce.Do(func() { close(w.closeCh) })
}
