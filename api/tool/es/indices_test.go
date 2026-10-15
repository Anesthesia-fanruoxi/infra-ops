package es

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func indicesTestRow(name string) map[string]any {
	return map[string]any{
		"index": name, "health": "green", "status": "open",
		"pri": "1", "rep": "0", "docs.count": "10", "docs.deleted": "0", "store.size": "1kb",
	}
}

// indicesTestGlob 把 ES 索引名通配（* / ?）翻成正则，供假服务端模拟「集群侧全量匹配」。
func indicesTestGlob(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range g {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// TestIndicesTotalCapAndSearch 覆盖 indices(ctx, pattern) 的三件事：
//  1. total 是「全部匹配的业务索引数」，且剔除 . 开头系统索引；
//  2. 返回列表截断到 indexListCap（100），但 total 如实反映总数；
//  3. pattern 透传进 /_cat/indices/{pattern} 由集群侧匹配，未命中（404）按 0 条处理。
func TestIndicesTotalCapAndSearch(t *testing.T) {
	var names []string
	for i := 0; i < 150; i++ {
		names = append(names, fmt.Sprintf("log-%03d", i))
	}
	names = append(names, ".kibana", ".security") // 系统索引，必须被剔除

	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		pattern := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/_cat/indices"), "/")
		if pattern == "__404__" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rows := make([]map[string]any, 0, len(names))
		for _, n := range names {
			if pattern != "" && !indicesTestGlob(pattern).MatchString(n) {
				continue
			}
			rows = append(rows, indicesTestRow(n))
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()

	c, err := newESCClient(srv.URL, true, "anonymous", "", "")
	if err != nil {
		t.Fatalf("newESCClient: %v", err)
	}
	ctx := context.Background()

	// 全量：total=150（剔除 2 个系统索引），list 截断到 100
	list, total, err := c.indices(ctx, "")
	if err != nil {
		t.Fatalf("indices(all): %v", err)
	}
	if total != 150 {
		t.Fatalf("total = %d, want 150（.kibana/.security 应被剔除）", total)
	}
	if len(list) != indexListCap {
		t.Fatalf("len(list) = %d, want %d（展示上限）", len(list), indexListCap)
	}
	for _, ix := range list {
		if strings.HasPrefix(ix.Index, ".") {
			t.Fatalf("列表里混入系统索引 %q", ix.Index)
		}
	}

	// 搜索：pattern 透传到 _cat 路径，由服务端在全部索引里按通配匹配
	list, total, err = c.indices(ctx, "log-00*")
	if err != nil {
		t.Fatalf("indices(log-00*): %v", err)
	}
	if !strings.Contains(lastPath, "log-00*") {
		t.Fatalf("pattern 未透传到 _cat 路径: %q", lastPath)
	}
	if total != 10 || len(list) != 10 {
		t.Fatalf("log-00* => total=%d len=%d, want 10/10", total, len(list))
	}

	// 通配未命中（部分 ES 版本回 404）按 0 条处理，不报错
	list, total, err = c.indices(ctx, "__404__")
	if err != nil {
		t.Fatalf("indices(404): %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("404 => total=%d len=%d, want 0/0", total, len(list))
	}
}

// TestIndicesTotalCountOnly 总数接口：只数业务索引（剔除 . 系统索引），
// 请求只取 h=index（不拉统计列，比列表请求轻）。
func TestIndicesTotalCountOnly(t *testing.T) {
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"index": "log-a"}, {"index": "log-b"}, {"index": ".kibana"},
		})
	}))
	defer srv.Close()
	c, err := newESCClient(srv.URL, true, "anonymous", "", "")
	if err != nil {
		t.Fatalf("newESCClient: %v", err)
	}
	n, err := c.indicesTotal(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("indicesTotal = %d, %v（应剔除 .kibana）", n, err)
	}
	if !strings.Contains(lastQuery, "h=index") {
		t.Fatalf("总数请求应只取 h=index: %q", lastQuery)
	}
}
