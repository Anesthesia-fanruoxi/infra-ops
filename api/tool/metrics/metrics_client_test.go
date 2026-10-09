package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func writeEnv(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// fakeProm 最小 prom 兼容端点：覆盖 query（vector / scalar / 错误）、query_range（matrix）、
// 指标清单、buildinfo、metadata、标签名与标签取值。
func fakeProm(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status/buildinfo", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": map[string]any{"version": "2.53.0"}})
	})
	mux.HandleFunc("/api/v1/label/__name__/values", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": []string{"up", "http_requests_total"}})
	})
	mux.HandleFunc("/api/v1/metadata", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": map[string]any{
			"up": []any{map[string]any{"type": "gauge", "help": "1 if the target is up"}},
		}})
	})
	mux.HandleFunc("/api/v1/labels", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": []string{"job", "project"}})
	})
	mux.HandleFunc("/api/v1/label/project/values", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": []string{"成享花", "云汉"}})
	})
	mux.HandleFunc("/api/v1/label/job/values", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": []string{"agent"}})
	})
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query") {
		case "up":
			writeEnv(w, map[string]any{"status": "success", "data": map[string]any{
				"resultType": "vector",
				"result": []any{map[string]any{
					"metric": map[string]string{"__name__": "up", "job": "api"},
					"value":  []any{1759968000.25, "1"},
				}},
			}})
		case "bad":
			w.WriteHeader(422)
			writeEnv(w, map[string]any{"status": "error", "errorType": "bad_data", "error": "invalid parameter"})
		default:
			writeEnv(w, map[string]any{"status": "success", "data": map[string]any{
				"resultType": "scalar", "result": []any{1759968000.5, "1"},
			}})
		}
	})
	mux.HandleFunc("/api/v1/query_range", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "warnings": []string{"test warning"},
			"data": map[string]any{"resultType": "matrix", "result": []any{
				map[string]any{
					"metric": map[string]string{"__name__": "up", "instance": "a"},
					"values": []any{[]any{1759968000.0, "1"}, []any{1759968030.0, "0"}},
				},
				map[string]any{
					"metric": map[string]string{"__name__": "up", "instance": "b"},
					"values": []any{[]any{1759968000.0, "NaN"}, []any{1759968030.0, "2.5"}},
				},
			}}})
	})
	return httptest.NewServer(mux)
}

func TestQueryVectorNormalize(t *testing.T) {
	srv := fakeProm(t)
	defer srv.Close()
	c, err := newPromClient(srv.URL, false, "none", "", "")
	if err != nil {
		t.Fatalf("newPromClient: %v", err)
	}
	res, err := c.query(context.Background(), "up", 1759968000250)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if res.ResultType != "vector" || len(res.Series) != 1 {
		t.Fatalf("resultType=%s series=%d", res.ResultType, len(res.Series))
	}
	pt := res.Series[0].Points[0]
	if pt.TS != 1759968000250 {
		t.Errorf("TS = %d, want 1759968000250", pt.TS)
	}
	if pt.V == nil || *pt.V != 1 {
		t.Errorf("V = %v, want 1", pt.V)
	}
	if res.Series[0].Metric["job"] != "api" {
		t.Errorf("metric job = %q", res.Series[0].Metric["job"])
	}
}

func TestQueryScalarNormalize(t *testing.T) {
	srv := fakeProm(t)
	defer srv.Close()
	c, _ := newPromClient(srv.URL, false, "none", "", "")
	res, err := c.query(context.Background(), "1", 1759968000500)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if res.ResultType != "scalar" || len(res.Series) != 1 {
		t.Fatalf("resultType=%s series=%d", res.ResultType, len(res.Series))
	}
	if pt := res.Series[0].Points[0]; pt.TS != 1759968000500 || pt.V == nil || *pt.V != 1 {
		t.Errorf("point = %+v", pt)
	}
}

func TestQueryRangeMatrixNormalize(t *testing.T) {
	srv := fakeProm(t)
	defer srv.Close()
	c, _ := newPromClient(srv.URL, false, "none", "", "")
	res, err := c.queryRange(context.Background(), "up", 1759968000000, 1759968030000, 30)
	if err != nil {
		t.Fatalf("queryRange: %v", err)
	}
	if len(res.Series) != 2 || res.Step != 30 {
		t.Fatalf("series=%d step=%d", len(res.Series), res.Step)
	}
	if !strings.Contains(strings.Join(res.Warnings, ";"), "test warning") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	a := res.Series[0].Points
	if len(a) != 2 || a[0].TS != 1759968000000 || a[1].TS != 1759968030000 {
		t.Errorf("points a = %+v", a)
	}
	if a[0].V == nil || *a[0].V != 1 {
		t.Errorf("a[0].V = %v", a[0].V)
	}
	// NaN 归一化为 nil（前端断线），不是解析错误
	b := res.Series[1].Points
	if b[0].V != nil {
		t.Errorf("b[0].V = %v, want nil (NaN)", b[0].V)
	}
	if b[1].V == nil || *b[1].V != 2.5 {
		t.Errorf("b[1].V = %v", b[1].V)
	}
}

func TestPromTimeFormat(t *testing.T) {
	if got := promTime(1759968000250); got != "1759968000.250" {
		t.Errorf("promTime = %q", got)
	}
}

func TestRemoteErrorEnvelope(t *testing.T) {
	srv := fakeProm(t)
	defer srv.Close()
	c, _ := newPromClient(srv.URL, false, "none", "", "")
	_, err := c.query(context.Background(), "bad", 0)
	if err == nil || !strings.Contains(err.Error(), "bad_data") {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, authType, user, secret, wantPrefix string
	}{
		{"basic", "basic", "u", "p", "Basic "},
		{"bearer", "bearer", "", "tok", "Bearer tok"},
	} {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("Authorization")
			writeEnv(w, map[string]any{"status": "success", "data": map[string]any{
				"resultType": "scalar", "result": []any{1, "1"},
			}})
		}))
		c, err := newPromClient(srv.URL, false, tc.authType, tc.user, tc.secret)
		if err != nil {
			t.Fatalf("%s: newPromClient: %v", tc.name, err)
		}
		if _, err := c.query(context.Background(), "1", 1000); err != nil {
			t.Fatalf("%s: query: %v", tc.name, err)
		}
		srv.Close()
		if !strings.HasPrefix(got, tc.wantPrefix) {
			t.Errorf("%s: Authorization = %q, want prefix %q", tc.name, got, tc.wantPrefix)
		}
	}
}

func TestMetricNamesAndPing(t *testing.T) {
	srv := fakeProm(t)
	defer srv.Close()
	c, _ := newPromClient(srv.URL, false, "none", "", "")
	names, err := c.metricNames(context.Background())
	if err != nil || len(names) != 2 {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	ver, err := c.ping(context.Background())
	if err != nil || ver != "2.53.0" {
		t.Fatalf("ping ver = %q, err = %v", ver, err)
	}
	meta, err := c.metadata(context.Background())
	if err != nil || len(meta["up"]) != 1 || meta["up"][0].Type != "gauge" {
		t.Fatalf("meta = %v, err = %v", meta, err)
	}
}

func TestPingFallbackToQuery(t *testing.T) {
	// 只有查询端点（无 buildinfo）时应回落 query=1 探测成功
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, _ *http.Request) {
		writeEnv(w, map[string]any{"status": "success", "data": map[string]any{
			"resultType": "scalar", "result": []any{1, "1"},
		}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, _ := newPromClient(srv.URL, false, "none", "", "")
	if _, err := c.ping(context.Background()); err != nil {
		t.Fatalf("ping fallback: %v", err)
	}
}
