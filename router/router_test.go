package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/store"
)

// setup 每个用例一个独立临时库：store.DB 是包级全局，用例之间必须互不干扰。
// 不能省这一步——Setup 内部会 new StackHandler 并跑 FailStaleRuns，
// 没有可用连接会直接 panic（不是返回错误）。
func setup(t *testing.T) *gin.Engine {
	t.Helper()
	if err := store.Open(filepath.Join(t.TempDir(), "router.db")); err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(store.Close)
	return Setup(nil, Deps{})
}

// errBody 解析统一响应结构。
func errBody(t *testing.T, w *httptest.ResponseRecorder) resp.R {
	t.Helper()
	var r resp.R
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("响应不是统一 JSON 结构: %v (body=%q)", err, w.Body.String())
	}
	return r
}

// TestNoRouteExplainsPath 路径不存在时必须自带可读说明。
// 起因是真实 404：/api/mysql/ai/config 上提为 /api/settings/ai 后前端漏改。
// gin 默认返回**空 body** 的 404，前端 axios 只能造一句
// `Request failed with status code 404` —— 界面上看不出是哪条路径对不上，
// 而桌面模式打不开 F12，这条提示就是唯一的线索。
func TestNoRouteExplainsPath(t *testing.T) {
	r := setup(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/mysql/ai/config", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("未注册路径应返回 404，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("404 响应应为 JSON（否则前端拿不到 message），实际 Content-Type=%q", ct)
	}
	body := errBody(t, w)
	if body.Code != resp.CodeNotFound {
		t.Errorf("业务码应为 CodeNotFound(%d)，实际 %d", resp.CodeNotFound, body.Code)
	}
	for _, want := range []string{"/api/mysql/ai/config", "GET", "接口不存在"} {
		if !strings.Contains(body.Message, want) {
			t.Errorf("404 message 应包含 %q（否则定位不到问题路径），实际: %q", want, body.Message)
		}
	}
}

// TestNoMethodExplainsMethod 方法写错要能被单独识别出来。
// 不开 HandleMethodNotAllowed 时它会退化成 404，用户会去查路径（而路径是对的）。
func TestNoMethodExplainsMethod(t *testing.T) {
	if !r405() {
		t.Fatal("路由未开启 HandleMethodNotAllowed，方法写错会被当成路径不存在")
	}
	r := setup(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/version", nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("/api/version 只支持 GET，POST 应返回 405，实际 %d", w.Code)
	}
	body := errBody(t, w)
	if !strings.Contains(body.Message, "方法不允许") || !strings.Contains(body.Message, "POST") {
		t.Errorf("405 message 应说明「方法不允许」并带上实际方法，实际: %q", body.Message)
	}
}

// r405 探测路由表是否开启了方法不匹配处理（避免直接依赖包级变量名）。
func r405() bool {
	r := Setup(nil, Deps{})
	return r.HandleMethodNotAllowed
}

// TestKnownRouteStillOK 加了 NoRoute/NoMethod 之后，已注册路由不能受影响。
func TestKnownRouteStillOK(t *testing.T) {
	r := setup(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/healthz"},
		{http.MethodGet, "/api/version"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s %s 应 200，实际 %d（body=%q）", tc.method, tc.path, w.Code, w.Body.String())
		}
		if body := errBody(t, w); body.Code != resp.CodeOK {
			t.Errorf("%s %s 业务码应为 0，实际 %d", tc.method, tc.path, body.Code)
		}
	}
}
