// cancel.go 正在执行的 SQL 的取消登记表：给「取消」按钮一个中断慢 SQL 的抓手。
//
// 执行请求是同步的（ctx 派生自 c.Request.Context），前端断开并不会让服务端的
// SQL 停跑（连接池里的连接还在服务端执行，只是没人收结果），所以「取消」必须
// 在服务端把 ctx 取消掉：go-sql-driver 监听 ctx，取消时直接断开底层连接，
// MySQL 服务端随即终止这条查询，等待中的 QueryContext / ExecContext 当场返回。
//
// 每次执行按连接登记一条凭据，执行请求收尾时注销；同一连接出现并发执行时
// 后登记的生效，旧凭据收尾不会误删新登记（finish 比对同一指针）。
package mysql

import (
	"context"
	"sync"
)

// queryCancel 一次执行中的查询的取消凭据。
type queryCancel struct {
	cancel context.CancelFunc
}

// queryCancelRegistry 每连接至多一条在跑查询的登记表。
type queryCancelRegistry struct {
	mu sync.Mutex
	m  map[int64]*queryCancel
}

func newQueryCancelRegistry() *queryCancelRegistry {
	return &queryCancelRegistry{m: map[int64]*queryCancel{}}
}

// register 登记本次执行，返回的凭据供 finish 注销用。
func (r *queryCancelRegistry) register(connID int64, cancel context.CancelFunc) *queryCancel {
	c := &queryCancel{cancel: cancel}
	r.mu.Lock()
	r.m[connID] = c
	r.mu.Unlock()
	return c
}

// finish 执行收尾：仍是自己才清，避免把后来者的登记误删。
func (r *queryCancelRegistry) finish(connID int64, c *queryCancel) {
	r.mu.Lock()
	if r.m[connID] == c {
		delete(r.m, connID)
	}
	r.mu.Unlock()
}

// cancel 中断该连接正在执行的查询；没有在执行返回 false。
func (r *queryCancelRegistry) cancel(connID int64) bool {
	r.mu.Lock()
	c := r.m[connID]
	r.mu.Unlock()
	if c == nil {
		return false
	}
	c.cancel()
	return true
}
