package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/redisclient"
	"infra-ops/common/resp"
	"infra-ops/model"
)

const (
	defaultScanLimit = 50   // 「首次」与「展示更多」单次返回条数
	maxScanLimit     = 500  // 单次调用条数上限（防手搓请求一次要十万条）
	scanAllKeys      = 5000 // 「展示全部」的条数上限
	scanCount        = 500  // 单轮 SCAN 的 COUNT 提示（遍历桶数，不是返回条数）
	scanRounds       = 200  // 单次请求最多推进几轮 SCAN
	scanAllRounds    = 2000 // 「展示全部」放宽的轮数

	scanKeepAlive   = 10 * time.Minute // 扫描会话空闲多久失效
	scanMaxSessions = 64               // 同时保留的扫描会话上限
)

// errNoMatchYet 「展示全部」在没有命中时被调用。
var errNoMatchYet = errors.New("尚无匹配结果")

// errEmptyPrefix 匹配串为空。留空会拼出 `*`，等价于 KEYS *——整个实例被阻塞，
// 所以这一条必须挡在入口，不能靠前端禁用按钮兜底。
var errEmptyPrefix = errors.New("匹配串为空")

// newScanSession 校验输入并构造一份新的扫描会话。
func newScanSession(connID int64, db int, prefix string) (*scanSession, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, errEmptyPrefix
	}
	if db < 0 {
		db = 0
	}
	return &scanSession{
		connID:  connID,
		db:      db,
		prefix:  prefix,
		pattern: matchPattern(prefix),
		cursor:  "0",
		seen:    map[string]struct{}{},
	}, nil
}

// scanSession 一份扫描游标状态。「一次匹配分多次取」靠它：
// SCAN 的游标必须原样带回下一轮，而游标可能重复返回同一个 key，故还要记住已见集合。
type scanSession struct {
	mu       sync.Mutex
	id       string
	connID   int64
	db       int
	prefix   string // 使用者输入原文
	pattern  string // 实际下发的 MATCH
	cursor   string
	seen     map[string]struct{}
	pending  []string // 已命中但尚未返回给前端的 key
	scanned  int64    // SCAN 返回的元素累计（含游标重复返回的）
	found    int64 // 去重后的命中数
	returned int64 // 已返回给前端的条数
	done     bool  // 游标已归零
	at       time.Time
	seq      int64 // 单调递增序号：淘汰时比「新旧」用，不依赖时钟精度
}

// scanRegistry 扫描会话表。桌面单机应用，用不着分布式存储，进程内即可。
type scanRegistry struct {
	mu  sync.Mutex
	m   map[string]*scanSession
	seq int64
}

func newScanRegistry() *scanRegistry {
	return &scanRegistry{m: map[string]*scanSession{}}
}

func (r *scanRegistry) save(s *scanSession) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.evictLocked(now)
	r.seq++
	s.id = newScanID()
	s.at = now
	s.seq = r.seq
	r.m[s.id] = s
	return s.id
}

func (r *scanRegistry) get(id string) (*scanSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.m[id]
	if !ok {
		return nil, false
	}
	if time.Since(s.at) > scanKeepAlive {
		delete(r.m, id)
		return nil, false
	}
	s.at = time.Now()
	return s, true
}

// dropByConn 删除某连接下的全部会话（删连接时调用，避免自增 ID 复用后继承旧游标）。
func (r *scanRegistry) dropByConn(connID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.m {
		if s.connID == connID {
			delete(r.m, id)
		}
	}
}

// evictLocked 先清过期，仍超上限时淘汰最久未访问的一个。
func (r *scanRegistry) evictLocked(now time.Time) {
	for id, s := range r.m {
		if now.Sub(s.at) > scanKeepAlive {
			delete(r.m, id)
		}
	}
	if len(r.m) < scanMaxSessions {
		return
	}
	// 按序号比新旧而不是按 at 比时间：Windows 的时钟精度只有毫秒级，
	// 连着建出来的会话 at 往往完全相同，用 Before 会一个都淘汰不掉。
	oldestID, oldestSeq := "", int64(0)
	for id, s := range r.m {
		if oldestID == "" || s.seq < oldestSeq {
			oldestSeq, oldestID = s.seq, id
		}
	}
	if oldestID != "" {
		delete(r.m, oldestID)
	}
}

func newScanID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// ---------------- 匹配串 ----------------

// matchPattern 把使用者输入拼成 SCAN 的 MATCH：前缀模糊匹配 = 字面量前缀 + `*`，
// 输入 `ops:` 匹配所有以 ops: 开头的 key。
//
// 前缀里的 glob 元字符一律转义，所以输入 `*` 只会匹配「真的以星号开头」的 key，
// 不会退化成 MATCH *（那等价于 KEYS *，会阻塞整个实例）。
func matchPattern(prefix string) string { return escapeGlob(prefix) + "*" }

// escapeGlob 给 Redis glob 元字符加反斜杠，把整串降级为字面量。
func escapeGlob(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---------------- 接口 ----------------

type scanReq struct {
	Prefix string `json:"prefix"`  // 新的匹配串（前缀匹配时必填）
	ScanID string `json:"scan_id"` // 已有扫描会话（「更多 / 全部」时带上）
	Limit  int    `json:"limit"`
	All    bool   `json:"all"` // 展示全部
	DB     *int   `json:"db"`  // 首次匹配时可指定库
}

// Keys POST /api/redis/:id/keys
//
// 一个接口承担三种动作：
//
//	不带 scan_id          —— 开一次新匹配（必须给 prefix），返回前 N 条
//	带 scan_id            —— 继续取 N 条（「展示更多」）
//	带 scan_id + all=true —— 一路取到上限（「展示全部」）
func (h *Handler) Keys(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req scanReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	sess, ok := h.resolveScan(c, conn, &req)
	if !ok {
		return
	}

	cli, ss, err := h.dial(conn, sess.db)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	defer ss.Close()

	res, err := h.runScan(c.Request.Context(), cli, sess, req.Limit, req.All)
	if err != nil {
		if errors.Is(err, errNoMatchYet) {
			resp.Fail(c, resp.CodeBadRequest, "当前匹配还没有结果；换一个更具体的匹配串再来展示全部")
			return
		}
		resp.Fail(c, resp.CodeInternal, "扫描失败: "+err.Error())
		return
	}
	resp.OK(c, res)
}

// resolveScan 取（或新建）扫描会话；失败时已写过响应。
func (h *Handler) resolveScan(c *gin.Context, conn *model.RedisConn, req *scanReq) (*scanSession, bool) {
	if req.ScanID == "" {
		db := conn.DefaultDB
		if req.DB != nil {
			db = *req.DB
		}
		s, err := newScanSession(conn.ID, db, req.Prefix)
		if err != nil {
			resp.Fail(c, resp.CodeBadRequest, "请输入要匹配的开头内容：留空等价于 KEYS *，会阻塞整个实例")
			return nil, false
		}
		h.scans.save(s)
		return s, true
	}
	s, ok := h.scans.get(req.ScanID)
	if !ok {
		resp.Fail(c, resp.CodeNotFound, "扫描会话已过期，请重新匹配")
		return nil, false
	}
	if s.connID != conn.ID {
		resp.Fail(c, resp.CodeBadRequest, "扫描会话与当前连接不匹配")
		return nil, false
	}
	return s, true
}

// runScan 推进扫描直到取够条数 / 扫完 / 触顶 / 超时。
func (h *Handler) runScan(ctx context.Context, cli *redisclient.Client, s *scanSession,
	limit int, all bool) (model.RedisScanResult, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	res := model.RedisScanResult{
		ScanID:  s.id,
		Prefix:  s.prefix,
		Pattern: s.pattern,
		DB:      s.db,
		Keys:    []model.RedisKeyItem{},
	}
	start := time.Now()

	// 「展示全部」只允许建立在已有匹配之上——否则它就是一个换了名字的 KEYS *。
	if all && s.found == 0 {
		return res, errNoMatchYet
	}

	maxKeys := normalizeScanLimit(limit)
	if all {
		maxKeys = scanAllKeys
	}
	res.MaxKeys = maxKeys

	rounds := scanRounds
	if all {
		rounds = scanAllRounds
	}
	deadline := start.Add(scanTimeout)

	// 一轮 SCAN 返回的匹配条数不受 COUNT 约束（COUNT 只是遍历桶数的提示），
	// 完全可能一次就超过本次要返回的条数。多出来的必须留进 pending 等下次取——
	// 一旦游标归零就再也取不回来了，那部分 key 会被静默吞掉。
	for !s.done && rounds > 0 && len(s.pending) < maxKeys {
		if time.Now().After(deadline) {
			break
		}
		rounds--
		next, keys, err := scanOnce(cli, s.cursor, s.pattern, scanCount)
		if err != nil {
			return res, err
		}
		s.cursor = next
		s.scanned += int64(len(keys))
		for _, k := range keys {
			if _, dup := s.seen[k]; dup {
				continue
			}
			s.seen[k] = struct{}{}
			s.found++
			s.pending = append(s.pending, k)
		}
		if next == "0" {
			s.done = true
		}
	}

	take := maxKeys
	if take > len(s.pending) {
		take = len(s.pending)
	}
	picked := append([]string(nil), s.pending[:take]...)
	s.pending = s.pending[take:]
	if len(s.pending) == 0 {
		s.pending = nil
	}

	// 拿到一批 key 名后再取 TYPE / TTL：一次 pipeline 换回，100 条只花一次往返。
	res.Keys = fetchKeyMeta(cli, picked)
	s.returned += int64(len(res.Keys))

	res.Scanned = s.scanned
	res.Found = s.found
	res.Returned = s.returned
	// 「扫完」= 游标归零 **且** 待发队列已空；只要还有攒着的 key 就仍可继续取
	res.Done = s.done && len(s.pending) == 0
	res.HasMore = !res.Done
	res.LimitHit = !res.Done && len(picked) >= maxKeys
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// scanOnce 推进一轮 SCAN，返回下一轮游标与本轮命中的 key。
func scanOnce(cli *redisclient.Client, cursor, pattern string, count int) (string, []string, error) {
	r, err := cli.Do("SCAN", cursor, "MATCH", pattern, "COUNT", count)
	if err != nil {
		return "", nil, err
	}
	if r.Kind != redisclient.KindArray || len(r.Array) < 2 {
		return "", nil, errors.New("SCAN 应答格式异常")
	}
	return r.Array[0].Text(), r.Array[1].Texts(), nil
}

// fetchKeyMeta 取回每条的 TYPE 与 TTL。
// 元数据取不到（连接抖动等）只丢类型与 TTL，key 名照列——总比整页空着强。
func fetchKeyMeta(cli *redisclient.Client, keys []string) []model.RedisKeyItem {
	items := make([]model.RedisKeyItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, model.RedisKeyItem{Key: k, TTL: -1})
	}
	if len(keys) == 0 {
		return items
	}
	cmds := make([][]any, 0, len(keys)*2)
	for _, k := range keys {
		cmds = append(cmds, []any{"TYPE", k}, []any{"TTL", k})
	}
	replies, err := cli.Pipeline(cmds)
	if err != nil {
		return items
	}
	for i := range items {
		if i*2 < len(replies) {
			items[i].Type = replies[i*2].Text()
		}
		if i*2+1 < len(replies) {
			if n, err := strconv.ParseInt(replies[i*2+1].Text(), 10, 64); err == nil {
				items[i].TTL = n
			}
		}
	}
	return items
}

func normalizeScanLimit(limit int) int {
	if limit <= 0 {
		return defaultScanLimit
	}
	if limit > maxScanLimit {
		return maxScanLimit
	}
	return limit
}
