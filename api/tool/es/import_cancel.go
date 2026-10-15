// import_cancel.go 导入任务的取消登记表。
//
// 导入是同步请求（ctx 派生自 c.Request.Context），前端断开并不会让服务端的导入停下，
// 所以「取消」必须在服务端把 ctx 取消掉：流式读取与 bulk 提交都会随 ctx 中断。
//
// key 用前端生成的 progress_key 而不是连接 ID：同一连接上并发发起两次导入时，
// 按连接登记会把前一次的凭据顶掉，取消就取消到别人头上。
package es

import (
	"context"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
)

// importCancelEntry 一次导入的取消凭据（包一层是为了能按指针比对）。
type importCancelEntry struct {
	cancel context.CancelFunc
}

type importCancelRegistry struct {
	mu sync.Mutex
	m  map[string]*importCancelEntry
}

func newImportCancelRegistry() *importCancelRegistry {
	return &importCancelRegistry{m: map[string]*importCancelEntry{}}
}

// register 登记本次导入，返回的凭据供 finish 注销用。
func (r *importCancelRegistry) register(key string, cancel context.CancelFunc) *importCancelEntry {
	if r == nil || key == "" {
		return nil
	}
	e := &importCancelEntry{cancel: cancel}
	r.mu.Lock()
	r.m[key] = e
	r.mu.Unlock()
	return e
}

// finish 收尾注销；仍是自己才清，避免把后来者的登记误删。
func (r *importCancelRegistry) finish(key string, e *importCancelEntry) {
	if r == nil || key == "" || e == nil {
		return
	}
	r.mu.Lock()
	if r.m[key] == e {
		delete(r.m, key)
	}
	r.mu.Unlock()
}

// cancel 中断该任务；没有在执行返回 false。
func (r *importCancelRegistry) cancel(key string) bool {
	if r == nil || key == "" {
		return false
	}
	r.mu.Lock()
	e := r.m[key]
	r.mu.Unlock()
	if e == nil {
		return false
	}
	e.cancel()
	return true
}

// ImportCancel POST /api/es/:id/import/cancel
// key 走 query 或 JSON body 都可以（前端用 query 更省事）。
func (h *Handler) ImportCancel(c *gin.Context) {
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		var req struct {
			Key string `json:"key"`
		}
		_ = c.ShouldBindJSON(&req)
		key = strings.TrimSpace(req.Key)
	}
	if key == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少任务标识 key")
		return
	}
	// 取消是异步生效的：这里只表明「已发出中断信号」，真正的收尾由导入请求返回结果。
	resp.OK(c, gin.H{"cancelled": h.cancels.cancel(key)})
}
