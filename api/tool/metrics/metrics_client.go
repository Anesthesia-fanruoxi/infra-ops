// metrics_client.go Prometheus / VictoriaMetrics HTTP 客户端：
// 请求构造、认证与响应归一化，供 handler 复用。
//
// 两者查询 API 同根同源（vector / matrix / scalar / string 四种结果形态一致），
// 一份实现通吃；URL 允许带路径前缀（VM 常挂在 /prometheus 下）。
package metrics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"infra-ops/model"
)

// 响应体大小上限：异常的大范围查询不该把进程拖垮（正常几百条序列的响应远小于此）。
const maxRespBytes = 64 << 20

// promClient 一个 Prometheus 兼容端点的 HTTP 客户端。
type promClient struct {
	base     string // 归一化后的 base URL（含路径前缀，无尾斜杠）
	authType string // none | basic | bearer
	username string
	secret   string
	hc       *http.Client
}

// newPromClient 构造客户端。
//
// 无 scheme 时默认 http:// —— 与 ES 工具相反：监控栈在内网几乎都是明文 HTTP
// （Prometheus / VM 默认不配 TLS），填 host:port 的常见写法按 http 处理;insecure
// 只控制 HTTPS 场景的证书校验跳过。
func newPromClient(rawURL string, insecure bool, authType, username, secret string) (*promClient, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil, fmt.Errorf("地址不能为空")
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	u = strings.TrimRight(u, "/")
	transport := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		MaxIdleConns:    8,
		MaxConnsPerHost: 8,
	}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式启用 insecure
	}
	return &promClient{
		base: u, authType: authType, username: username, secret: secret,
		hc: &http.Client{Timeout: 60 * time.Second, Transport: transport},
	}, nil
}

// promEnvelope 远端统一响应信封（prom 与 VM 一致）。
type promEnvelope struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
	Warnings  []string        `json:"warnings"`
	Stats     json.RawMessage `json:"stats"` // 2.x 新版 / VM 的查询统计块，不是所有版本都有
}

// do 发起 GET 请求并解析信封；HTTP 错误与 prom error 统一转成可读错误。
func (c *promClient) do(ctx context.Context, path string, q url.Values) (*promEnvelope, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	switch c.authType {
	case "basic":
		req.SetBasicAuth(c.username, c.secret)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("认证失败（401），请检查用户名密码 / Bearer Token")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("无权限（403）")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("端点不存在（404）: %s", path)
	}
	env := &promEnvelope{}
	if err := json.Unmarshal(data, env); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(data))
		}
		return nil, fmt.Errorf("响应不是 Prometheus 格式: %s", snippet(data))
	}
	if env.Status != "success" {
		msg := strings.TrimSpace(env.Error)
		if msg == "" {
			msg = "远端返回失败"
		}
		if env.ErrorType != "" {
			msg = env.ErrorType + ": " + msg
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return env, nil
}

// ping 探测连通性并尽力取回版本：先 buildinfo（prom / VM 新版都有），
// 失败回落 query=1（个别代理裁剪了状态端点，但查询可用）。版本取不到也不算失败。
func (c *promClient) ping(ctx context.Context) (string, error) {
	env, err := c.do(ctx, "/api/v1/status/buildinfo", nil)
	if err == nil {
		var info struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(env.Data, &info) == nil && info.Version != "" {
			return info.Version, nil
		}
		return "", nil
	}
	// buildinfo 不可用（旧版 / 代理裁剪 / 权限限制）时用一次瞬时查询兜底
	q := url.Values{}
	q.Set("query", "1")
	if _, qerr := c.do(ctx, "/api/v1/query", q); qerr != nil {
		return "", err // 报第一次的错误：更能说明端点形态问题
	}
	return "", nil
}

// metricNames 全部指标名（/api/v1/label/__name__/values）。
func (c *promClient) metricNames(ctx context.Context) ([]string, error) {
	env, err := c.do(ctx, "/api/v1/label/__name__/values", nil)
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(env.Data, &names); err != nil {
		return nil, fmt.Errorf("解析指标清单失败: %w", err)
	}
	return names, nil
}

// metricMeta 指标元信息（/api/v1/metadata 的单个条目）。
type metricMeta struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

// metadata 指标元信息表；不支持该端点的集群返回空表（生成时仅凭指标名也能用）。
func (c *promClient) metadata(ctx context.Context) (map[string][]metricMeta, error) {
	env, err := c.do(ctx, "/api/v1/metadata", nil)
	if err != nil {
		return map[string][]metricMeta{}, nil
	}
	var out map[string][]metricMeta
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return map[string][]metricMeta{}, nil
	}
	return out, nil
}

// labelNames 标签名全集（/api/v1/labels）；端点不支持的报错由调用方忽略。
func (c *promClient) labelNames(ctx context.Context) ([]string, error) {
	env, err := c.do(ctx, "/api/v1/labels", nil)
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(env.Data, &names); err != nil {
		return nil, fmt.Errorf("解析标签列表失败: %w", err)
	}
	return names, nil
}

// labelValues 某标签的取值全集（/api/v1/label/<name>/values）。
func (c *promClient) labelValues(ctx context.Context, name string) ([]string, error) {
	env, err := c.do(ctx, "/api/v1/label/"+url.PathEscape(name)+"/values", nil)
	if err != nil {
		return nil, err
	}
	var vals []string
	if err := json.Unmarshal(env.Data, &vals); err != nil {
		return nil, fmt.Errorf("解析标签取值失败: %w", err)
	}
	return vals, nil
}

// query 瞬时查询（instant）。
func (c *promClient) query(ctx context.Context, expr string, atMs int64) (*model.MetricsQueryResult, error) {
	q := url.Values{}
	q.Set("query", expr)
	q.Set("time", promTime(atMs))
	start := time.Now()
	env, err := c.do(ctx, "/api/v1/query", q)
	if err != nil {
		return nil, err
	}
	series, rt, err := normalizePromResult(env.Data)
	if err != nil {
		return nil, err
	}
	res := &model.MetricsQueryResult{
		Expr: expr, Mode: "instant",
		Start: atMs, End: atMs,
		ResultType: rt, Series: series,
		Warnings:  env.Warnings,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	fillStats(res, env.Stats)
	return res, nil
}

// queryRange 范围查询（range）；stepSec 为步长秒。
func (c *promClient) queryRange(ctx context.Context, expr string, startMs, endMs, stepSec int64) (*model.MetricsQueryResult, error) {
	q := url.Values{}
	q.Set("query", expr)
	q.Set("start", promTime(startMs))
	q.Set("end", promTime(endMs))
	q.Set("step", strconv.FormatInt(stepSec, 10))
	start := time.Now()
	env, err := c.do(ctx, "/api/v1/query_range", q)
	if err != nil {
		return nil, err
	}
	series, rt, err := normalizePromResult(env.Data)
	if err != nil {
		return nil, err
	}
	res := &model.MetricsQueryResult{
		Expr: expr, Mode: "range",
		Start: startMs, End: endMs, Step: stepSec,
		ResultType: rt, Series: series,
		Warnings:  env.Warnings,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	fillStats(res, env.Stats)
	return res, nil
}

// fillStats 透传远端 stats 块（结构随版本而异，原样带给前端）。
func fillStats(res *model.MetricsQueryResult, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil && len(m) > 0 {
		res.Stats = m
	}
}

// normalizePromResult 把远端 data（resultType + result）归一化成统一序列形态。
func normalizePromResult(data json.RawMessage) ([]model.MetricsSeries, string, error) {
	var d struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, "", fmt.Errorf("解析查询结果失败: %w", err)
	}
	switch d.ResultType {
	case "vector":
		var rows []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		}
		if err := json.Unmarshal(d.Result, &rows); err != nil {
			return nil, "", fmt.Errorf("解析 vector 结果失败: %w", err)
		}
		out := make([]model.MetricsSeries, 0, len(rows))
		for _, r := range rows {
			out = append(out, model.MetricsSeries{
				Metric: orEmptyMap(r.Metric),
				Points: []model.MetricsPoint{parsePoint(r.Value)},
			})
		}
		return out, "vector", nil
	case "matrix":
		var rows []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		}
		if err := json.Unmarshal(d.Result, &rows); err != nil {
			return nil, "", fmt.Errorf("解析 matrix 结果失败: %w", err)
		}
		out := make([]model.MetricsSeries, 0, len(rows))
		for _, r := range rows {
			pts := make([]model.MetricsPoint, 0, len(r.Values))
			for _, p := range r.Values {
				pts = append(pts, parsePoint(p))
			}
			out = append(out, model.MetricsSeries{Metric: orEmptyMap(r.Metric), Points: pts})
		}
		return out, "matrix", nil
	case "scalar", "string":
		// 这两种形态的 result 是 [ts, val] 二元组而非序列数组；造一条无名序列承载。
		var pair []any
		if err := json.Unmarshal(d.Result, &pair); err != nil {
			return nil, "", fmt.Errorf("解析 %s 结果失败: %w", d.ResultType, err)
		}
		return []model.MetricsSeries{{
			Metric: map[string]string{},
			Points: []model.MetricsPoint{parsePoint(pair)},
		}}, d.ResultType, nil
	default:
		return nil, d.ResultType, fmt.Errorf("未知的结果类型: %q", d.ResultType)
	}
}

// parsePoint 解析一个 [ts, value] 样本。
func parsePoint(pair []any) model.MetricsPoint {
	pt := model.MetricsPoint{}
	if len(pair) != 2 {
		return pt
	}
	if f, ok := pair[0].(float64); ok {
		pt.TS = int64(math.Round(f * 1000)) // 秒（小数）→ 毫秒，四舍五入防浮点截断误差
	}
	pt.V = parseSampleValue(pair[1])
	return pt
}

// parseSampleValue prom 的样本值是字符串（防浮点精度丢失）；
// NaN / ±Inf / 解析失败归一化为 nil，前端据此断线而不是画错点。
func parseSampleValue(v any) *float64 {
	switch t := v.(type) {
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return &f
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil
		}
		return &t
	}
	return nil
}

// promTime 毫秒时间戳 → prom 查询参数（Unix 秒，保留毫秒小数）。
func promTime(ms int64) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)
}

// snippet 截断响应体用于错误展示。
func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		s = "(空响应)"
	}
	return s
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
