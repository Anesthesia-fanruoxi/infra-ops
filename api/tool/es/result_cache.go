// 结果集内存缓存（B8，§11）：一次检索拉满 1000 条存进程内存，翻页只读内存、不访问 ES。
// 键 conn_id+view_id，新查询直接覆盖；result_id 防「旧页码读新结果」；
// 全局三上限（20 集 / 20000 条 / 64MB）超限 LRU 淘汰；空闲 TTL 30 分钟。不落 SQLite。
package es

import (
	"fmt"
	"sync"
	"time"
)

const (
	esCacheMaxSets  = 20
	esCacheMaxDocs  = 20000
	esCacheMaxBytes = 64 << 20 // 64MB（按序列化长度估算）
	esCacheTTL      = 30 * time.Minute
	esPageMaxSize   = 1000
)

// cachedHit 结果集中的一条命中（已整形，供分页直接切片返回）。
type cachedHit struct {
	Index     string                 `json:"index"`
	ID        string                 `json:"id"`
	Score     interface{}            `json:"score"`
	Source    map[string]interface{} `json:"source"`
	Highlight map[string]interface{} `json:"highlight,omitempty"`
}

// resultSet 一次查询的完整结果窗口。
type resultSet struct {
	ResultID   string
	ConnID     int64
	ViewID     int64
	Total      int64
	TimeField  string
	StartMS    int64
	EndMS      int64
	Columns    []string
	Hits       []cachedHit
	Histogram  []histBucket
	CreatedAt  time.Time
	LastAccess time.Time
}

// histBucket 时间直方图桶。
type histBucket struct {
	Key         int64  `json:"key"`
	KeyAsString string `json:"key_as_string,omitempty"`
	DocCount    int64  `json:"doc_count"`
}

// resultCache 进程内结果集缓存，单把 RWMutex（§11.2）。
type resultCache struct {
	mu   sync.RWMutex
	sets map[string]*resultSet // key: conn-view
	seq  int64
}

var cache = &resultCache{sets: map[string]*resultSet{}}

// cachePut 存入 / 覆盖同一键上的旧结果，并按三上限做 LRU 淘汰。
func cachePut(rs *resultSet) {
	rs.CreatedAt = time.Now()
	rs.LastAccess = rs.CreatedAt
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.seq++
	rs.ResultID = fmt.Sprintf("%d-%d-%d", rs.ConnID, rs.ViewID, cache.seq)
	cache.sets[cacheKey(rs.ConnID, rs.ViewID)] = rs
	cache.evictLocked()
}

func cacheKey(connID, viewID int64) string { return fmt.Sprintf("%d-%d", connID, viewID) }

// evictLocked 先淘汰过期（TTL），再按三上限逐出最久未访问者。
func (c *resultCache) evictLocked() {
	now := time.Now()
	for k, rs := range c.sets {
		if now.Sub(rs.LastAccess) > esCacheTTL {
			delete(c.sets, k)
		}
	}
	for {
		if len(c.sets) <= esCacheMaxSets && c.totalDocs() <= esCacheMaxDocs && c.totalBytes() <= esCacheMaxBytes {
			return
		}
		if len(c.sets) == 0 {
			return
		}
		// 找 LastAccess 最老的淘汰（保护刚放入的集：它最新，天然不会被选中）
		oldestKey := ""
		var oldest time.Time
		for k, rs := range c.sets {
			if oldestKey == "" || rs.LastAccess.Before(oldest) {
				oldestKey, oldest = k, rs.LastAccess
			}
		}
		delete(c.sets, oldestKey)
	}
}

func (c *resultCache) totalDocs() int {
	n := 0
	for _, rs := range c.sets {
		n += len(rs.Hits)
	}
	return n
}

func (c *resultCache) totalBytes() int {
	n := 0
	for _, rs := range c.sets {
		n += len(rs.Columns) * 32
		n += len(rs.Hits) * 4096 // 每条命中按 ~4KB 估算（§11.2）
		n += len(rs.Histogram) * 48
	}
	return n
}

// cacheGet 读缓存（命中则刷新 LastAccess）。
func cacheGet(connID, viewID int64) *resultSet {
	cache.mu.RLock()
	rs := cache.sets[cacheKey(connID, viewID)]
	cache.mu.RUnlock()
	if rs != nil {
		cache.mu.Lock()
		rs.LastAccess = time.Now()
		cache.mu.Unlock()
	}
	return rs
}

// cachePage 从内存结果集切片（不访问 ES）。
// result_id 不匹配 → 4011(409)；越界 → 4005(400)。
func cachePage(connID, viewID int64, resultID string, from, size int) ([]cachedHit, int64, *dslErr) {
	if size <= 0 || size > esPageMaxSize {
		size = 20
	}
	rs := cacheGet(connID, viewID)
	if rs == nil || rs.ResultID != resultID {
		return nil, 0, &dslErr{code: esCodeResultStale,
			msg: "结果集已更新或已失效，请重新查询"}
	}
	total := int64(len(rs.Hits))
	if from < 0 || int64(from) >= total && total > 0 || from > 0 && total == 0 {
		return nil, total, &dslErr{code: esCodePageOutOfRange,
			msg: fmt.Sprintf("翻页越界：窗口 %d 条，请求起点 %d", total, from)}
	}
	end := from + size
	if end > len(rs.Hits) {
		end = len(rs.Hits)
	}
	return rs.Hits[from:end], total, nil
}
