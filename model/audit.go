package model

// AuditLog 操作审计记录：记录何时做了什么、结果如何（HTTP 状态与业务码/消息）。
type AuditLog struct {
	ID         int64  `json:"id"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   int64  `json:"target_id"`
	HTTPStatus int    `json:"http_status"`
	Code       int    `json:"code"`
	Message    string `json:"message"`
	CreatedAt  string `json:"created_at"`
}
