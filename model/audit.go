package model

// AuditLog 操作审计记录。
type AuditLog struct {
	ID         int64  `json:"id"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   int64  `json:"target_id"`
	Detail     string `json:"detail"`
	RemoteIP   string `json:"remote_ip"`
	CreatedAt  string `json:"created_at"`
}
