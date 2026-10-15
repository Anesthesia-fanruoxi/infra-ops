// cache.go 编译缓存。
//
// 使用场景决定了它值得存在：Grok 面板是「改一行 pattern → 点一次解析」的调参循环，
// 同一份 pattern 会被反复编译（每次请求都要重编译一遍正则，还要重走 %{...} 展开）。
// 键是「pattern 列表 + 定义表」的内容摘要，命中即复用，出错也缓存（同样的输入必然同样报错）。
package grok

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// cacheMax 缓存条目上限：粗粒度保护，满了整体清空而不是做 LRU —— 调参场景的热点很小。
const cacheMax = 256

type cacheEntry struct {
	m   *Matcher
	err error
}

var (
	cache     sync.Map
	cacheSize atomic.Int64
)

// Cached 带缓存的 Compile。
func Cached(patterns []string, defs map[string]string) (*Matcher, error) {
	key := cacheKey(patterns, defs)
	if v, ok := cache.Load(key); ok {
		e := v.(*cacheEntry)
		return e.m, e.err
	}
	m, err := Compile(patterns, defs)
	if cacheSize.Add(1) > cacheMax {
		cache.Range(func(k, _ interface{}) bool { cache.Delete(k); return true })
		cacheSize.Store(1)
	}
	cache.Store(key, &cacheEntry{m: m, err: err})
	return m, err
}

// cacheKey 把 pattern 与定义拍成确定性字符串（定义按键排序，避免 map 遍历顺序影响命中）。
func cacheKey(patterns []string, defs map[string]string) string {
	var b strings.Builder
	for _, p := range patterns {
		b.WriteString(strings.TrimSpace(p))
		b.WriteByte(0x1f)
	}
	keys := make([]string, 0, len(defs))
	for k := range defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(defs[k])
		b.WriteByte(0x1f)
	}
	return b.String()
}
