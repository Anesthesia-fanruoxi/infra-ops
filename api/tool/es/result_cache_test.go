// B8 验收：缓存覆盖 / LRU 淘汰 / TTL / 4011 / 4005 单测。
package es

import (
	"fmt"
	"testing"
	"time"
)

func mkResultSet(connID, viewID int64, docs int) *resultSet {
	rs := &resultSet{ConnID: connID, ViewID: viewID, Total: int64(docs), TimeField: "@timestamp"}
	for i := 0; i < docs; i++ {
		rs.Hits = append(rs.Hits, cachedHit{Index: "i", ID: string(rune('a' + i%26))})
	}
	return rs
}

// 新查询直接覆盖同一键上的旧结果；旧 result_id → 4011。
func TestCacheOverwriteAndStale(t *testing.T) {
	c := &resultCache{sets: map[string]*resultSet{}}
	c.mu.Lock()
	c.seq = 0
	c.mu.Unlock()

	rs1 := mkResultSet(1, 1, 3)
	put(c, rs1)
	id1 := rs1.ResultID
	if id1 == "" {
		t.Fatal("应生成 result_id")
	}
	rs2 := mkResultSet(1, 1, 5)
	put(c, rs2)
	if rs2.ResultID == id1 {
		t.Fatal("覆盖后应生成新 result_id")
	}
	// 旧 result_id → 4011
	if _, _, err := page(c, 1, 1, id1, 0, 10); err == nil || err.code != esCodeResultStale {
		t.Fatalf("旧 result_id 应 4011: %v", err)
	}
	// 新 result_id 正常切片
	hits, total, err := page(c, 1, 1, rs2.ResultID, 2, 10)
	if err != nil {
		t.Fatalf("分页失败: %v", err)
	}
	if total != 5 || len(hits) != 3 {
		t.Fatalf("切片错误: total=%d len=%d", total, len(hits))
	}
}

// LRU：超过集数上限时淘汰最久未访问者。
func TestCacheLRUEvict(t *testing.T) {
	c := &resultCache{sets: map[string]*resultSet{}}
	var firstID string
	for i := 0; i <= esCacheMaxSets; i++ { // 放入 21 个集
		rs := mkResultSet(1, int64(i+1), 1)
		put(c, rs)
		if i == 0 {
			firstID = rs.ResultID
		}
		time.Sleep(time.Millisecond) // 保证 LastAccess 单调
	}
	if _, _, err := page(c, 1, 1, firstID, 0, 1); err == nil || err.code != esCodeResultStale {
		t.Fatalf("最久未访问的集应被淘汰: %v", err)
	}
	if len(c.sets) > esCacheMaxSets {
		t.Fatalf("集数超上限: %d", len(c.sets))
	}
}

// TTL：空闲超时后淘汰。
func TestCacheTTL(t *testing.T) {
	c := &resultCache{sets: map[string]*resultSet{}}
	rs := mkResultSet(1, 1, 1)
	put(c, rs)
	c.mu.Lock()
	rs.LastAccess = time.Now().Add(-2 * esCacheTTL)
	c.mu.Unlock()
	rs2 := mkResultSet(1, 2, 1)
	put(c, rs2) // 触发淘汰检查
	if _, _, err := page(c, 1, 1, rs.ResultID, 0, 1); err == nil || err.code != esCodeResultStale {
		t.Fatalf("TTL 过期应被淘汰: %v", err)
	}
}

// 4005：翻页越界。
func TestCachePageOutOfRange(t *testing.T) {
	c := &resultCache{sets: map[string]*resultSet{}}
	rs := mkResultSet(1, 1, 10)
	put(c, rs)
	if _, _, err := page(c, 1, 1, rs.ResultID, 10, 20); err == nil || err.code != esCodePageOutOfRange {
		t.Fatalf("起点越界应 4005: %v", err)
	}
	// 边界：起点恰为最后一条 → 合法
	if _, _, err := page(c, 1, 1, rs.ResultID, 9, 5); err != nil {
		t.Fatalf("末条起点应合法: %v", err)
	}
}

// 独立缓存实例的 put/page 助手（cachePut/cachePage 绑定全局 cache，便于单测隔离）。
func put(c *resultCache, rs *resultSet) {
	rs.CreatedAt = time.Now()
	rs.LastAccess = rs.CreatedAt
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	rs.ResultID = fmt.Sprintf("%d-%d-%d", rs.ConnID, rs.ViewID, c.seq)
	c.sets[fmt.Sprintf("%d-%d", rs.ConnID, rs.ViewID)] = rs
	c.evictLocked()
}

func page(c *resultCache, connID, viewID int64, resultID string, from, size int) ([]cachedHit, int64, *dslErr) {
	if size <= 0 || size > esPageMaxSize {
		size = 20
	}
	c.mu.RLock()
	rs := c.sets[fmt.Sprintf("%d-%d", connID, viewID)]
	c.mu.RUnlock()
	if rs != nil {
		c.mu.Lock()
		rs.LastAccess = time.Now()
		c.mu.Unlock()
	}
	if rs == nil || rs.ResultID != resultID {
		return nil, 0, &dslErr{code: esCodeResultStale, msg: "结果集已更新或已失效，请重新查询"}
	}
	total := int64(len(rs.Hits))
	if from < 0 || (total > 0 && int64(from) >= total) || (total == 0 && from > 0) {
		return nil, total, &dslErr{code: esCodePageOutOfRange,
			msg: fmt.Sprintf("翻页越界：窗口 %d 条，请求起点 %d", total, from)}
	}
	end := from + size
	if end > len(rs.Hits) {
		end = len(rs.Hits)
	}
	return rs.Hits[from:end], total, nil
}
