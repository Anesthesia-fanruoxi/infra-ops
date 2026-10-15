package middleware

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/model"
	"infra-ops/store/repo"
)

// maxCaptureBytes 响应体捕获上限，超出部分不参与结果解析（避免大响应占用内存）。
const maxCaptureBytes = 64 << 10

// bodyCapture 包装 ResponseWriter，暂存响应体用于解析业务结果。
type bodyCapture struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *bodyCapture) Write(b []byte) (int, error) {
	if w.body.Len() < maxCaptureBytes {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *bodyCapture) WriteString(s string) (int, error) {
	if w.body.Len() < maxCaptureBytes {
		w.body.WriteString(s)
	}
	return w.ResponseWriter.WriteString(s)
}

// Audit 审计中间件：对全部写操作（POST/PUT/PATCH/DELETE）落库，
// 记录「何时做了什么、是否报错、返回了什么」；不记录操作者与来源地址。
func Audit(r *repo.AuditRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
			return
		}

		cap := &bodyCapture{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = cap
		c.Next()

		action := resolveAction(c.FullPath(), method)
		if action == "" {
			return
		}

		code, message := parseResult(cap.body.Bytes())
		entry := &model.AuditLog{
			Action:     action,
			TargetType: resolveTargetType(c.FullPath()),
			TargetID:   resolveTargetID(c),
			HTTPStatus: c.Writer.Status(),
			Code:       code,
			Message:    message,
		}
		if err := r.Create(entry); err != nil {
			log.Printf("audit log write failed: %v", err)
		}
	}
}

// parseResult 从响应体解析业务结果：同步响应走 resp 统一结构（code/message），
// 流式响应（SSE）取最终帧——final 帧即成功、error 帧即失败；
// 两者都不是时回落空值，仅保留 HTTP 状态可判定成败。
func parseResult(body []byte) (int, string) {
	if len(body) == 0 {
		return 0, ""
	}
	if code, message, ok := parseSSEFrames(body); ok {
		return code, message
	}
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return 0, ""
	}
	return r.Code, r.Message
}

// parseSSEFrames 从 SSE 响应体里提取业务结果：逐帧向后扫描，
// 以最后一个 final / error 帧为准（AI 生成类接口的终稿与失败都在这两类帧里）。
// 响应被捕获上限截断、恰好没截到最终帧时返回 ok=false，由调用方按未知结果处理。
func parseSSEFrames(body []byte) (int, string, bool) {
	event := ""
	code, message, found := 0, "", false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if name, ok := strings.CutPrefix(line, "event:"); ok {
			event = strings.TrimSpace(name)
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		switch event {
		case "final":
			code, message, found = 0, "ok", true
		case "error":
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(strings.TrimSpace(data)), &e)
			code, message, found = 500, e.Message, true
		}
	}
	return code, message, found
}

// resolveAction 生成动作标识 "METHOD /path"（剥离 /api 前缀，参数位保留 :id 形态）；非 API 路径返回空。
func resolveAction(fullPath, method string) string {
	if !strings.HasPrefix(fullPath, "/api/") {
		return ""
	}
	return method + " " + strings.TrimPrefix(fullPath, "/api")
}

// resolveTargetType 按一级路径归组业务模块，供界面筛选。
func resolveTargetType(fullPath string) string {
	path := strings.TrimPrefix(fullPath, "/api/")
	seg, _, _ := strings.Cut(path, "/")
	return seg
}

// resolveTargetID 提取 :id 路径参数（仅接受纯数字）。
func resolveTargetID(c *gin.Context) int64 {
	idStr := c.Param("id")
	if idStr == "" {
		return 0
	}
	var id int64
	for _, ch := range idStr {
		if ch < '0' || ch > '9' {
			return 0
		}
		id = id*10 + int64(ch-'0')
	}
	return id
}
