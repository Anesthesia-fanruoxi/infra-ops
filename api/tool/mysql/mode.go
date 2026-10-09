// Package mysql 工具-MySQL：会话运行模式的进程内状态（门禁的判断 1）。
package mysql

import (
	"sync"

	"infra-ops/model"
)

// modeRegistry 会话运行模式的进程内状态。
//
// 本工具的每次请求都会新建并在结束时关闭 sql.DB，没有长连接会话可挂载状态，
// 因此模式按连接 ID 记在这里：打开工作台时由连接的默认模式初始化，
// 之后使用者可随时切换；进程重启后回落到连接配置的默认模式。
type modeRegistry struct {
	mu   sync.RWMutex
	mode map[int64]string
}

func newModeRegistry() *modeRegistry {
	return &modeRegistry{mode: map[int64]string{}}
}

// get 取当前模式；从未设置过则用 fallback（连接配置的默认模式）初始化并返回。
func (r *modeRegistry) get(connID int64, fallback string) string {
	r.mu.RLock()
	m, ok := r.mode[connID]
	r.mu.RUnlock()
	if ok {
		return m
	}
	r.set(connID, fallback)
	return fallback
}

func (r *modeRegistry) set(connID int64, mode string) {
	r.mu.Lock()
	r.mode[connID] = mode
	r.mu.Unlock()
}

// drop 删除连接的模式状态；连接被删掉时调用，避免 ID 复用后继承旧模式。
func (r *modeRegistry) drop(connID int64) {
	r.mu.Lock()
	delete(r.mode, connID)
	r.mu.Unlock()
}

// defaultMode 连接的默认模式：只读连接进入工作台时默认为 read。
func defaultMode(conn *model.MySQLConn) string {
	if conn.ReadOnly {
		return ModeRead
	}
	return ModeWrite
}

// confirmLevel 确认分级，供前端选择弹窗强度：
//
//	readonly —— 只读模式下由使用者逐条放行，警示最强；
//	ddl      —— 可写模式下改结构 / 未知语句，不可回滚；
//	write    —— 可写模式下改数据。
func confirmLevel(mode, sqlType string) string {
	if mode != ModeWrite {
		return "readonly"
	}
	if sqlRisk(sqlType) >= 2 {
		return "ddl"
	}
	return "write"
}
