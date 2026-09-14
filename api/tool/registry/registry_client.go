// Registry v2 HTTP 客户端：连接构造、请求执行与 token 刷新。
package registry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type registryClient struct {
	base       string
	username   string
	password   string
	scheme     string
	token      string
	tokenExp   time.Time
	httpClient *http.Client
}

func newRegistryClient(rawURL string, insecure bool, authType, username, password string) (*registryClient, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil, fmt.Errorf("地址不能为空")
	}
	scheme := ""
	if strings.Contains(u, "://") {
		parsed, err := url.Parse(u)
		if err != nil {
			return nil, err
		}
		scheme = parsed.Scheme
		u = parsed.Host + parsed.Path
	} else {
		if insecure {
			scheme = "http"
		} else {
			scheme = "https"
		}
	}
	u = strings.TrimRight(u, "/")

	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式启用 insecure
	}

	user := ""
	if authType == "basic" {
		user = username
	}
	return &registryClient{
		base:       scheme + "://" + u,
		username:   user,
		password:   password,
		scheme:     scheme,
		httpClient: &http.Client{Timeout: 20 * time.Second, Transport: transport},
	}, nil
}

func (rc *registryClient) endpoint(path string) string {
	return rc.base + path
}

// do 执行一次请求：携带 Basic 认证；若遇 401 Bearer 则走 token 流程重试。
func (rc *registryClient) do(ctx context.Context, method, path string, header http.Header, body io.Reader) (*http.Response, []byte, error) {
	attempts := 0
	for {
		attempts++
		req, err := http.NewRequestWithContext(ctx, method, rc.endpoint(path), body)
		if err != nil {
			return nil, nil, err
		}
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		if rc.username != "" {
			req.SetBasicAuth(rc.username, rc.password)
		} else if rc.token != "" && time.Now().Before(rc.tokenExp) {
			req.Header.Set("Authorization", "Bearer "+rc.token)
		}

		resp, err := rc.httpClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		// token 流程仅在匿名不带 Basic 时参与；Basic 直接以其本身为准
		if resp.StatusCode == http.StatusUnauthorized && rc.username == "" && attempts < 2 {
			challenge := resp.Header.Get("WWW-Authenticate")
			_ = resp.Body.Close()
			if err := rc.tryRefreshToken(ctx, challenge, path); err != nil {
				return nil, nil, err
			}
			continue
		}
		data, err := io.ReadAll(resp.Body)
		if req.Body != nil {
			_ = req.Body.Close()
		}
		_ = resp.Body.Close()
		if err != nil {
			return resp, nil, err
		}
		return resp, data, nil
	}
}

// tryRefreshToken 解析 WWW-Authenticate 的 Bearer challenge 并换取 token。
func (rc *registryClient) tryRefreshToken(ctx context.Context, challenge, path string) error {
	// 形如: Bearer realm="https://auth.hub...",service="registry",scope="..."
	realm := ""
	params := map[string]string{}
	for _, part := range strings.Split(challenge, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		params[kv[0]] = strings.Trim(kv[1], `"`)
	}
	if strings.HasPrefix(challenge, "Bearer") {
		realm = params["realm"]
	} else if strings.HasPrefix(challenge, "Basic") {
		return fmt.Errorf("需要 Basic 认证，请填写用户名密码")
	}
	if realm == "" {
		return fmt.Errorf("无法解析仓库认证方式")
	}
	// token URL 的 scheme 跟随仓库本身（insecure 时用 http）
	realm = strings.Replace(realm, "https:", rc.scheme+":", 1)

	q := url.Values{}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	scope := params["scope"]
	if scope == "" {
		if scope = scopeForPath(path); scope != "" {
			q.Set("scope", scope)
		}
	} else {
		q.Set("scope", scope)
	}
	tokenURL := realm + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return err
	}
	if rc.username != "" {
		req.SetBasicAuth(rc.username, rc.password)
	}
	respHTTP, err := rc.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer respHTTP.Body.Close()
	body, _ := io.ReadAll(respHTTP.Body)
	if respHTTP.StatusCode >= 400 {
		return fmt.Errorf("获取访问令牌失败(%d): %s", respHTTP.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	_ = json.Unmarshal(body, &tok)
	rc.token = tok.Token
	if rc.token == "" {
		rc.token = tok.AccessToken
	}
	if rc.token == "" {
		return fmt.Errorf("返回内容缺少访问令牌")
	}
	exp := tok.ExpiresIn
	if exp <= 0 {
		exp = 60
	}
	rc.tokenExp = time.Now().Add(time.Duration(exp) * time.Second)
	return nil
}

func scopeForPath(path string) string {
	if strings.HasPrefix(path, "/v2/_catalog") {
		return "registry:catalog:*"
	}
	if strings.HasPrefix(path, "/v2/") {
		rest := strings.TrimSuffix(strings.TrimPrefix(path, "/v2/"), "/")
		name := rest
		if i := strings.Index(rest, "/manifests/"); i >= 0 {
			name = rest[:i]
		} else if i := strings.Index(rest, "/tags/"); i >= 0 {
			name = rest[:i]
		}
		if strings.Contains(path, "DELETE") || strings.Contains(path, "/manifests/") {
			return fmt.Sprintf("repository:%s:pull,push", name)
		}
		return fmt.Sprintf("repository:%s:pull", name)
	}
	return ""
}

// normalizeRegistryReq 规整 URL 与缺省字段。
func normalizeRegistryReq(req *registryUpsertReq) {
	if req.AuthType == "" {
		req.AuthType = "anonymous"
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimSpace(req.Username)
	req.URL = strings.TrimSpace(req.URL)
	if req.AuthType != "basic" {
		req.Username = ""
		req.Password = ""
	}
}

func authTypeOrDefault(t string) string {
	if t == "basic" {
		return "basic"
	}
	return "anonymous"
}
