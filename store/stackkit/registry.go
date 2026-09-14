package stackkit

import (
	"fmt"
	"sync"
)

// Registry 套件驱动注册表容器：按注册顺序保留，List 顺序即对外展示顺序
// （前端套件卡片顺序、蓝图列表顺序都由它决定）。
type Registry struct {
	mu    sync.RWMutex
	order []string
	byKey map[string]Driver
}

// NewRegistry 建一个空注册表。
func NewRegistry() *Registry {
	return &Registry{byKey: map[string]Driver{}}
}

// Register 登记一个驱动。空驱动、空 key、重复 key 都是装配期编程错误，直接报错
// 由调用方（store/registry.go）fail fast。
func (r *Registry) Register(d Driver) error {
	if d == nil {
		return fmt.Errorf("stackkit: 登记了空驱动")
	}
	key := d.Key()
	if key == "" {
		return fmt.Errorf("stackkit: 驱动 key 为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byKey[key]; ok {
		return fmt.Errorf("stackkit: 套件 %s 重复登记", key)
	}
	r.byKey[key] = d
	r.order = append(r.order, key)
	return nil
}

// Find 按 key 取驱动；不存在返回 nil。
func (r *Registry) Find(key string) Driver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

// List 按登记顺序返回全部驱动。
func (r *Registry) List() []Driver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Driver, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.byKey[k])
	}
	return out
}

// Keys 按登记顺序返回全部套件 key。
func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Default 全局注册表：由 store/registry.go 的装配表填充，套件目录自查时可直接引用。
var Default = NewRegistry()

// Register 登记到 Default。
func Register(d Driver) error { return Default.Register(d) }

// Find 从 Default 查找。
func Find(key string) Driver { return Default.Find(key) }

// List 按登记顺序返回 Default 的全部驱动。
func List() []Driver { return Default.List() }

// Keys 按登记顺序返回 Default 的全部套件 key。
func Keys() []string { return Default.Keys() }
