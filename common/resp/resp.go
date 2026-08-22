// Package resp 提供统一 JSON 响应结构与错误码常量。
package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 错误码常量。
const (
	CodeOK             = 0
	CodeBadRequest     = 400
	CodeUnauthorized   = 401
	CodeForbidden      = 403
	CodeNotFound       = 404
	CodeConflict       = 409
	CodeInternal       = 500
	CodeSSHConnFail    = 1001
	CodeSSHAuthFail    = 1002
	CodeSSHHostKey     = 1003
	CodeSSHCollectFail = 1004
)

// R 统一响应结构。
type R struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// OK 返回成功响应。
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, R{Code: CodeOK, Message: "ok", Data: data})
}

// Fail 返回业务失败（HTTP 200，code 区分）。
func Fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, R{Code: code, Message: msg, Data: nil})
}

// ErrHTTP 返回 HTTP 状态码级别的错误。
func ErrHTTP(c *gin.Context, httpCode int, code int, msg string) {
	c.JSON(httpCode, R{Code: code, Message: msg, Data: nil})
}

// PageData 分页响应结构。
type PageData struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}
