package middleware

import (
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/model"
	"infra-ops/store"
)

// auditWriter 审计日志写入接口（避免循环依赖）。
type auditWriter interface {
	Create(log *model.AuditLog) error
}

// Audit 审计中间件：拦截写操作，响应成功后落库。
func Audit(repo *store.AuditRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 先执行 handler
		c.Next()

		// 只对写操作记录审计
		method := c.Request.Method
		if method != "POST" && method != "PUT" && method != "DELETE" {
			return
		}

		// 只记录成功的写操作（200 且 code=0）或业务操作
		if c.Writer.Status() >= 500 {
			return
		}

		action := resolveAction(c.FullPath(), method)
		if action == "" {
			return
		}

		username, _ := c.Get("username")
		detail := buildDetail(c, action)

		auditLog := &model.AuditLog{
			Action:     action,
			TargetType: resolveTargetType(c.FullPath()),
			TargetID:   resolveTargetID(c),
			Detail:     detail,
			RemoteIP:   c.ClientIP(),
		}
		if username != nil {
			auditLog.Detail = "user=" + username.(string) + " " + detail
		}

		if err := repo.Create(auditLog); err != nil {
			log.Printf("audit log write failed: %v", err)
		}
	}
}

func resolveAction(fullPath, method string) string {
	// ???????? /api/v1 ???????? /api ???
	path := strings.TrimPrefix(fullPath, "/api/v1")
	path = strings.TrimPrefix(path, "/api")

	mapping := map[string]map[string]string{
		"POST": {
			"/auth/logout":    "auth.logout",
			"/credentials":    "credential.create",
			"/hosts":          "host.create",
			"/hosts/batch":    "host.batch_create",
			"/hosts/:id/test": "host.test",
		},
		"PUT": {
			"/credentials/:id": "credential.update",
			"/hosts/:id":       "host.update",
		},
		"DELETE": {
			"/credentials/:id": "credential.delete",
			"/hosts/:id":       "host.delete",
		},
	}
	if actions, ok := mapping[method]; ok {
		return actions[path]
	}
	return ""
}

func resolveTargetType(fullPath string) string {
	switch {
	case strings.Contains(fullPath, "credentials"):
		return "credential"
	case strings.Contains(fullPath, "hosts"):
		return "host"
	case strings.Contains(fullPath, "auth"):
		return "auth"
	default:
		return ""
	}
}

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

func buildDetail(c *gin.Context, action string) string {
	parts := []string{}
	parts = append(parts, "action="+action)
	parts = append(parts, "time="+time.Now().Format("15:04:05"))
	return strings.Join(parts, " ")
}
