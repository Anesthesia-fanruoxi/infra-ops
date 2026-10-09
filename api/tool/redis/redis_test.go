package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"infra-ops/common/redisclient"
)

// ---------------- 纯函数部分 ----------------

func TestMatchPatternEscapesGlob(t *testing.T) {
	cases := []struct{ prefix, want string }{
		{"order", "order*"},
		{"order:123", "order:123*"},
		{"中文前缀", "中文前缀*"},
		// 输入里的 glob 元字符必须是字面量：否则 `*` 会变成 MATCH *（= KEYS *）
		{"*", `\**`},
		{"a*b", `a\*b*`},
		{"a?b", `a\?b*`},
		{"a[b]c", `a\[b\]c*`},
		{`a\b`, `a\\b*`},
	}
	for _, tc := range cases {
		if got := matchPattern(tc.prefix); got != tc.want {
			t.Errorf("matchPattern(%q) = %q, want %q", tc.prefix, got, tc.want)
		}
	}
	if matchPattern("") != "*" {
		t.Fatal("空匹配串会拼出裸 * —— 这正是必须在入口拒绝它的原因")
	}
}

func TestNewScanSession(t *testing.T) {
	if _, err := newScanSession(1, 0, "   "); !errors.Is(err, errEmptyPrefix) {
		t.Fatalf("空白匹配串应被拒: %v", err)
	}
	if _, err := newScanSession(1, 0, "\t\n"); !errors.Is(err, errEmptyPrefix) {
		t.Fatalf("制表符/换行也应被拒: %v", err)
	}
	s, err := newScanSession(7, -3, "  order  ")
	if err != nil {
		t.Fatalf("newScanSession: %v", err)
	}
	if s.prefix != "order" || s.pattern != "order*" || s.cursor != "0" || s.db != 0 || s.connID != 7 {
		t.Fatalf("会话字段不符: %+v", s)
	}
	if s.seen == nil {
		t.Fatal("seen 必须初始化，否则首轮去重会 panic")
	}
}

func TestScanRegistryExpiryAndDrop(t *testing.T) {
	r := newScanRegistry()
	s, _ := newScanSession(1, 0, "a")
	id := r.save(s)
	if got, ok := r.get(id); !ok || got != s {
		t.Fatal("刚存的会话应当能取回")
	}

	// 人为把访问时间推到过期之前
	r.mu.Lock()
	r.m[id].at = time.Now().Add(-scanKeepAlive - time.Minute)
	r.mu.Unlock()
	if _, ok := r.get(id); ok {
		t.Fatal("过期会话应当失效")
	}

	s2, _ := newScanSession(2, 0, "b")
	id2 := r.save(s2)
	s3, _ := newScanSession(2, 0, "c")
	id3 := r.save(s3)
	r.dropByConn(2)
	if _, ok := r.get(id2); ok {
		t.Fatal("dropByConn 应当清掉该连接的全部会话")
	}
	if _, ok := r.get(id3); ok {
		t.Fatal("dropByConn 应当清掉该连接的全部会话")
	}
}

func TestScanRegistryEvictsOldest(t *testing.T) {
	r := newScanRegistry()
	ids := make([]string, 0, scanMaxSessions+5)
	for i := 0; i < scanMaxSessions+5; i++ {
		s, _ := newScanSession(1, 0, fmt.Sprintf("k%d", i))
		ids = append(ids, r.save(s))
	}
	r.mu.Lock()
	n := len(r.m)
	r.mu.Unlock()
	if n > scanMaxSessions {
		t.Fatalf("会话数 %d 超过上限 %d", n, scanMaxSessions)
	}
	if _, ok := r.get(ids[0]); ok {
		t.Fatal("最旧的会话应当已被淘汰")
	}
}

func TestNormalizeScanLimit(t *testing.T) {
	cases := []struct{ in, want int }{{0, defaultScanLimit}, {-5, defaultScanLimit}, {10, 10}, {maxScanLimit, maxScanLimit}, {99999, maxScanLimit}}
	for _, tc := range cases {
		if got := normalizeScanLimit(tc.in); got != tc.want {
			t.Errorf("normalizeScanLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseKeyspace(t *testing.T) {
	text := "# Keyspace\r\ndb0:keys=12,expires=3,avg_ttl=0\r\ndb3:keys=1,expires=0\r\nnot-a-db:foo\r\n"
	list := parseKeyspace(text)
	if len(list) != 2 {
		t.Fatalf("应解析出 2 个库，得到 %d：%+v", len(list), list)
	}
	if list[0].Index != 0 || list[0].Keys != 12 || list[0].Expires != 3 {
		t.Fatalf("db0 解析不符: %+v", list[0])
	}
	if list[1].Index != 3 || list[1].Keys != 1 {
		t.Fatalf("db3 解析不符: %+v", list[1])
	}
}

func TestServerInfo(t *testing.T) {
	text := "# Server\r\nredis_version:7.2.4\r\nredis_mode:standalone\r\nos:Linux\r\n"
	version, mode := serverInfo(text)
	if version != "7.2.4" || mode != "standalone" {
		t.Fatalf("serverInfo = (%q, %q)", version, mode)
	}
}

// ---------------- 扫描流程（对着假 Redis 跑） ----------------

func TestRunScanPaginatesWithoutDuplicates(t *testing.T) {
	f := newFakeRedis(t)
	// key 必须以匹配串开头——本工具是「前缀模糊匹配」，`order` 只能命中 `order:...` 这类 key
	for i := 0; i < 130; i++ {
		f.set(fmt.Sprintf("order:%03d:evt", i), &fakeValue{typ: "string", str: "x", ttl: -1})
	}
	f.set("user:1", &fakeValue{typ: "string", str: "u", ttl: -1})
	// 让假服务端的 SCAN 故意重复返回上一批，验证去重（真实 SCAN 游标允许重复）
	f.overlap = true

	cli := f.client(t)
	h := NewHandler(Deps{})
	s, err := newScanSession(1, 0, "order")
	if err != nil {
		t.Fatalf("newScanSession: %v", err)
	}

	first, err := h.runScan(context.Background(), cli, s, 50, false)
	if err != nil {
		t.Fatalf("runScan: %v", err)
	}
	if len(first.Keys) != 50 {
		t.Fatalf("首批应返回 50 条，得到 %d", len(first.Keys))
	}
	if first.Done || !first.HasMore {
		t.Fatalf("130 条里还有更多，done=%v has_more=%v", first.Done, first.HasMore)
	}
	if first.Pattern != "order*" || first.Prefix != "order" {
		t.Fatalf("匹配串回显不符: %+v", first)
	}
	for _, k := range first.Keys {
		if !strings.HasPrefix(k.Key, "order") {
			t.Fatalf("返回了不匹配的 key: %s", k.Key)
		}
		if k.Type != "string" || k.TTL != -1 {
			t.Fatalf("元数据不符: %+v", k)
		}
	}

	seen := map[string]bool{}
	for _, k := range first.Keys {
		seen[k.Key] = true
	}
	second, err := h.runScan(context.Background(), cli, s, 50, false)
	if err != nil {
		t.Fatalf("第二轮 runScan: %v", err)
	}
	if len(second.Keys) != 50 {
		t.Fatalf("第二批应返回 50 条，得到 %d", len(second.Keys))
	}
	for _, k := range second.Keys {
		if seen[k.Key] {
			t.Fatalf("第二批出现重复 key: %s", k.Key)
		}
	}
	if second.Returned != 100 {
		t.Fatalf("累计返回应为 100，得到 %d", second.Returned)
	}

	// 第三轮取完剩余 30 条
	third, err := h.runScan(context.Background(), cli, s, 50, false)
	if err != nil {
		t.Fatalf("第三轮 runScan: %v", err)
	}
	if len(third.Keys) != 30 {
		t.Fatalf("剩余应为 30 条，得到 %d", len(third.Keys))
	}
	if !third.Done || third.HasMore {
		t.Fatalf("扫完应当 done=true has_more=false，得到 done=%v has_more=%v", third.Done, third.HasMore)
	}
	if third.Found != 130 {
		t.Fatalf("命中总数应为 130（不含 user:1，去重后），得到 %d", third.Found)
	}
}

func TestRunScanStopsAtLimit(t *testing.T) {
	f := newFakeRedis(t)
	for i := 0; i < 300; i++ {
		f.set(fmt.Sprintf("sess:%03d", i), &fakeValue{typ: "string", str: "v", ttl: 60})
	}
	cli := f.client(t)
	h := NewHandler(Deps{})
	s, _ := newScanSession(1, 0, "sess")

	res, err := h.runScan(context.Background(), cli, s, 20, false)
	if err != nil {
		t.Fatalf("runScan: %v", err)
	}
	if len(res.Keys) != 20 {
		t.Fatalf("应恰好返回 20 条，得到 %d", len(res.Keys))
	}
	if !res.LimitHit {
		t.Fatal("命中条数上限时应置 limit_hit")
	}
	if res.MaxKeys != 20 {
		t.Fatalf("max_keys 应为 20，得到 %d", res.MaxKeys)
	}
}

// 「展示全部」必须先有匹配——否则它就是换了名字的 KEYS *。
func TestRunScanAllRequiresExistingMatch(t *testing.T) {
	h := NewHandler(Deps{})
	s, _ := newScanSession(1, 0, "nothing")
	if _, err := h.runScan(context.Background(), nil, s, 50, true); !errors.Is(err, errNoMatchYet) {
		t.Fatalf("无命中时展示全部应被拒，得到 %v", err)
	}
}

func TestRunScanAllCapsAtLimit(t *testing.T) {
	f := newFakeRedis(t)
	f.overlap = false
	// 首次匹配会先取走 defaultScanLimit 条，剩下的必须多于上限才谈得上「触顶」
	total := scanAllKeys + defaultScanLimit + 150
	for i := 0; i < total; i++ {
		f.set(fmt.Sprintf("log:%05d", i), &fakeValue{typ: "string", str: "v", ttl: -1})
	}
	cli := f.client(t)
	h := NewHandler(Deps{})
	s, _ := newScanSession(1, 0, "log")

	// 先做一次普通匹配拿到命中，才能申请「展示全部」
	first, err := h.runScan(context.Background(), cli, s, 50, false)
	if err != nil {
		t.Fatalf("首次 runScan: %v", err)
	}
	if len(first.Keys) == 0 {
		t.Fatal("首次匹配应有结果")
	}
	all, err := h.runScan(context.Background(), cli, s, 50, true)
	if err != nil {
		t.Fatalf("展示全部: %v", err)
	}
	if len(all.Keys) > scanAllKeys {
		t.Fatalf("展示全部返回 %d 条，超过上限 %d", len(all.Keys), scanAllKeys)
	}
	if all.MaxKeys != scanAllKeys {
		t.Fatalf("max_keys 应为 %d，得到 %d", scanAllKeys, all.MaxKeys)
	}
	if !all.LimitHit {
		t.Fatal("keyspace 比上限大，应当置 limit_hit")
	}
	// 全部返回的 key 仍必须符合匹配串
	for _, k := range all.Keys {
		if !strings.HasPrefix(k.Key, "log") {
			t.Fatalf("返回了不匹配的 key: %s", k.Key)
		}
	}
}

// ---------------- 值预览 ----------------

func TestReadKeyByType(t *testing.T) {
	f := newFakeRedis(t)
	f.set("s1", &fakeValue{typ: "string", str: "hello world", ttl: 120})
	f.set("l1", &fakeValue{typ: "list", list: []string{"a", "b", "c"}, ttl: -1})
	f.set("z1", &fakeValue{typ: "zset", zset: []string{"m1", "1.5", "m2", "2.5"}, ttl: -1})
	f.set("h1", &fakeValue{typ: "hash", hash: []string{"f1", "v1", "f2", "v2"}, ttl: -1})
	f.set("st1", &fakeValue{typ: "stream", ttl: -1, stream: []fakeStreamItem{{id: "1-0", fields: []string{"k", "v"}}}})

	cli := f.client(t)

	t.Run("string", func(t *testing.T) {
		det, err := readKey(cli, "s1")
		if err != nil {
			t.Fatalf("readKey: %v", err)
		}
		if det.Type != "string" || det.Preview != "hello world" || det.Size != 11 || det.TTL != 120 {
			t.Fatalf("string 详情不符: %+v", det)
		}
		if det.Truncated {
			t.Fatal("短字符串不应标记截断")
		}
	})

	t.Run("list", func(t *testing.T) {
		det, err := readKey(cli, "l1")
		if err != nil {
			t.Fatalf("readKey: %v", err)
		}
		if det.Type != "list" || det.Size != 3 || len(det.Entries) != 3 {
			t.Fatalf("list 详情不符: %+v", det)
		}
		if det.Entries[1].Rank != "1" || det.Entries[1].Value != "b" {
			t.Fatalf("list 元素序号/值不符: %+v", det.Entries[1])
		}
	})

	t.Run("zset", func(t *testing.T) {
		det, err := readKey(cli, "z1")
		if err != nil {
			t.Fatalf("readKey: %v", err)
		}
		if det.Size != 2 || len(det.Entries) != 2 {
			t.Fatalf("zset 详情不符: %+v", det)
		}
		// WITHSCORES 是 member/score 交替，rank 位置放分数、value 放成员
		if det.Entries[0].Value != "m1" || det.Entries[0].Rank != "1.5" {
			t.Fatalf("zset 成员/分数位置颠倒: %+v", det.Entries[0])
		}
	})

	t.Run("hash", func(t *testing.T) {
		det, err := readKey(cli, "h1")
		if err != nil {
			t.Fatalf("readKey: %v", err)
		}
		if det.Size != 2 || len(det.Entries) != 2 {
			t.Fatalf("hash 详情不符: %+v", det)
		}
		if det.Entries[1].Field != "f2" || det.Entries[1].Value != "v2" {
			t.Fatalf("hash 字段/值不符: %+v", det.Entries[1])
		}
	})

	t.Run("stream", func(t *testing.T) {
		det, err := readKey(cli, "st1")
		if err != nil {
			t.Fatalf("readKey: %v", err)
		}
		if det.Size != 1 || len(det.Entries) != 1 {
			t.Fatalf("stream 详情不符: %+v", det)
		}
		if det.Entries[0].Rank != "1-0" || det.Entries[0].Value != "k=v" {
			t.Fatalf("stream ID/内容不符: %+v", det.Entries[0])
		}
	})

	t.Run("missing", func(t *testing.T) {
		det, err := readKey(cli, "nope")
		if err != nil {
			t.Fatalf("readKey: %v", err)
		}
		if det.Type != "none" {
			t.Fatalf("不存在的 key 应为 none，得到 %q", det.Type)
		}
	})
}

// ---------------- 只读承诺 ----------------

// 本工具对使用者承诺「只读」：只会下发读命令。这条承诺靠命令白名单锁住——
// 以后有人顺手在某个分支里加一条 DEL / SET / FLUSHDB，这里会红，而不是等数据没了才发现。
func TestOnlyReadCommands(t *testing.T) {
	allowed := map[string]bool{
		"PING": true, "AUTH": true, "SELECT": true, "INFO": true, "DBSIZE": true,
		"SCAN": true, "TYPE": true, "TTL": true,
		"STRLEN": true, "GETRANGE": true,
		"LLEN": true, "LRANGE": true,
		"SCARD": true, "SSCAN": true,
		"ZCARD": true, "ZRANGE": true,
		"HLEN": true, "HSCAN": true,
		"XLEN": true, "XRANGE": true,
	}
	files := []string{"redis.go", "redis_scan.go", "redis_value.go"}
	// 只抓「每个命令的首个字符串」：cli.Do("CMD", …) 与 []any{"CMD", …}（含 [][]any{{"CMD"）。
	// 这样 SCAN 的 MATCH / COUNT、ZRANGE 的 WITHSCORES 这类参数不会被误当成命令。
	cmdRe := regexp.MustCompile(`(?:\.Do\(|\[\]any\{\{?)"([A-Z]+)"`)
	seen := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s: %v", f, err)
		}
		for _, m := range cmdRe.FindAllStringSubmatch(string(src), -1) {
			cmd := m[1]
			seen[cmd] = true
			if !allowed[cmd] {
				t.Errorf("%s 下发了非只读命令 %s —— 本工具承诺只读，新增命令前必须先确认它是读命令并加进白名单", f, cmd)
			}
		}
	}
	// 防空扫：正则写坏时上面一条都不会红，白名单就形同虚设。
	if len(seen) < 10 {
		t.Fatalf("只扫到 %d 条命令（%v），命令匹配正则可能已失效", len(seen), seen)
	}
}

// ---------------- 假 Redis 服务端 ----------------

type fakeStreamItem struct {
	id     string
	fields []string
}

type fakeValue struct {
	typ    string
	str    string
	list   []string
	set    []string
	zset   []string // member, score 交替
	hash   []string // field, value 交替
	stream []fakeStreamItem
	ttl    int64
}

type fakeRedis struct {
	ln      net.Listener
	mu      sync.Mutex
	order   []string
	data    map[string]*fakeValue
	overlap bool
}

func newFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeRedis{ln: ln, data: map[string]*fakeValue{}}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeRedis) set(key string, v *fakeValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[key]; !ok {
		f.order = append(f.order, key)
	}
	f.data[key] = v
}

func (f *fakeRedis) client(t *testing.T) *redisclient.Client {
	t.Helper()
	cli, err := redisclient.Dial(context.Background(), redisclient.Options{
		Addr:    f.ln.Addr().String(),
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial fake redis: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func (f *fakeRedis) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		args, err := readCmd(br)
		if err != nil {
			return
		}
		if _, err := io.WriteString(conn, f.exec(args)); err != nil {
			return
		}
	}
}

func (f *fakeRedis) exec(args []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch strings.ToUpper(args[0]) {
	case "PING":
		return "+PONG\r\n"
	case "TYPE":
		if v := f.data[args[1]]; v != nil {
			return "+" + v.typ + "\r\n"
		}
		return "+none\r\n"
	case "TTL":
		if v := f.data[args[1]]; v != nil {
			return integer(v.ttl)
		}
		return ":-2\r\n"
	case "STRLEN":
		if v := f.data[args[1]]; v != nil && v.typ == "string" {
			return integer(int64(len(v.str)))
		}
		return ":-2\r\n"
	case "GETRANGE":
		v := f.data[args[1]]
		if v == nil {
			return "$-1\r\n"
		}
		return bulk(sliceStr(v.str, atoi(args[2]), atoi(args[3])))
	case "LLEN":
		if v := f.data[args[1]]; v != nil {
			return integer(int64(len(v.list)))
		}
		return ":-2\r\n"
	case "LRANGE":
		v := f.data[args[1]]
		if v == nil {
			return array(nil)
		}
		return array(sliceList(v.list, atoi(args[2]), atoi(args[3])))
	case "SCARD":
		if v := f.data[args[1]]; v != nil {
			return integer(int64(len(v.set)))
		}
		return ":-2\r\n"
	case "SSCAN":
		v := f.data[args[1]]
		if v == nil {
			return "*2\r\n" + bulk("0") + array(nil)
		}
		return "*2\r\n" + bulk("0") + array(v.set)
	case "ZCARD":
		if v := f.data[args[1]]; v != nil {
			return integer(int64(len(v.zset) / 2))
		}
		return ":-2\r\n"
	case "ZRANGE":
		v := f.data[args[1]]
		if v == nil {
			return array(nil)
		}
		// args[2] / args[3] 是成员下标，要换算成 member/score 交替数组的下标
		members := len(v.zset) / 2
		start, stop := atoi(args[2]), atoi(args[3])
		if start > members {
			return array(nil)
		}
		if stop >= members {
			stop = members - 1
		}
		return array(v.zset[start*2 : (stop+1)*2])
	case "HLEN":
		if v := f.data[args[1]]; v != nil {
			return integer(int64(len(v.hash) / 2))
		}
		return ":-2\r\n"
	case "HSCAN":
		v := f.data[args[1]]
		if v == nil {
			return "*2\r\n" + bulk("0") + array(nil)
		}
		return "*2\r\n" + bulk("0") + array(v.hash)
	case "XLEN":
		if v := f.data[args[1]]; v != nil {
			return integer(int64(len(v.stream)))
		}
		return ":-2\r\n"
	case "XRANGE":
		v := f.data[args[1]]
		if v == nil {
			return array(nil)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(v.stream))
		for _, it := range v.stream {
			b.WriteString("*2\r\n")
			b.WriteString(bulk(it.id))
			b.WriteString(array(it.fields))
		}
		return b.String()
	case "SCAN":
		return f.scan(args)
	}
	return "-ERR unknown command '" + args[0] + "'\r\n"
}

// scan 用「数组下标当游标」模拟真实 SCAN：分批返回、批内过滤、游标归零表示结束。
// overlap 打开时每批与上一批重叠一个元素，用来复现「SCAN 可能重复返回」这一真实特性。
func (f *fakeRedis) scan(args []string) string {
	cursor := atoi(args[1])
	pattern, count := "", 10
	for i := 2; i+1 < len(args); i += 2 {
		switch strings.ToUpper(args[i]) {
		case "MATCH":
			pattern = args[i+1]
		case "COUNT":
			count = atoi(args[i+1])
		}
	}
	start := cursor
	if f.overlap && start > 0 {
		start--
	}
	end := start + count
	if end > len(f.order) {
		end = len(f.order)
	}
	if start > len(f.order) {
		start = len(f.order)
	}
	next := end
	if next >= len(f.order) {
		next = 0
	}
	matched := []string{}
	for _, k := range f.order[start:end] {
		if globStartsWith(pattern, k) {
			matched = append(matched, k)
		}
	}
	return "*2\r\n" + bulk(strconv.Itoa(next)) + array(matched)
}

// globStartsWith 是测试侧的独立参照实现：本工具只会下发「转义字面量前缀 + *」，
// 所以只判「是否以该字面量开头」。与生产侧的 matchPattern 互为对照。
func globStartsWith(pattern, key string) bool {
	if !strings.HasSuffix(pattern, "*") {
		return false
	}
	return strings.HasPrefix(key, unescapeGlob(pattern[:len(pattern)-1]))
}

func unescapeGlob(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if esc {
			b.WriteRune(r)
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func sliceStr(s string, start, end int) string {
	if start > len(s) {
		return ""
	}
	if end >= len(s) {
		end = len(s) - 1
	}
	if end < start {
		return ""
	}
	return s[start : end+1]
}

func sliceList(list []string, start, stop int) []string {
	if start > len(list) {
		return nil
	}
	if stop >= len(list) {
		stop = len(list) - 1
	}
	return list[start : stop+1]
}

func bulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

func array(items []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(items))
	for _, it := range items {
		b.WriteString(bulk(it))
	}
	return b.String()
}

func integer(n int64) string { return ":" + strconv.FormatInt(n, 10) + "\r\n" }

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// readCmd 解析一条客户端命令（仅测试用）。
func readCmd(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, io.ErrUnexpectedEOF
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		head, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimRight(head[1:], "\r\n"))
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		out = append(out, string(buf[:size]))
	}
	return out, nil
}
