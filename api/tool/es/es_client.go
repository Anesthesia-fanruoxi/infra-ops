// ES HTTP 客户端：连接构造、请求执行与响应错误解析，供 handler 复用。
package es

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type escClient struct {
	base       string
	username   string
	password   string
	httpClient *http.Client
}

func newESCClient(rawURL string, insecure bool, authType, username, password string) (*escClient, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil, fmt.Errorf("地址不能为空")
	}
	if !strings.Contains(u, "://") {
		if insecure {
			u = "http://" + u
		} else {
			u = "https://" + u
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
	return &escClient{
		base: u, username: user, password: password,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}, nil
}

// do 发起 HTTP 请求，返回状态码与响应体。
func (c *escClient) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, data, err
	}
	return resp.StatusCode, data, nil
}

// esErr 从 ES 错误响应中提取可读原因。
func esErr(status int, body []byte, fallback string) error {
	if status == http.StatusUnauthorized {
		return fmt.Errorf("认证失败，请检查用户名密码")
	}
	if status == http.StatusForbidden {
		return fmt.Errorf("无权限执行该操作(403)")
	}
	var e struct {
		Error struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Reason != "" {
		reason := e.Error.Reason
		if len(reason) > 300 {
			reason = reason[:300]
		}
		return fmt.Errorf("%s(%d): %s", fallback, status, reason)
	}
	if len(body) > 0 {
		s := strings.TrimSpace(string(body))
		if len(s) > 200 {
			s = s[:200]
		}
		return fmt.Errorf("%s(%d): %s", fallback, status, s)
	}
	return fmt.Errorf("%s(%d)", fallback, status)
}

func (c *escClient) ping(ctx context.Context) error {
	status, body, err := c.do(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return esErr(status, body, "HTTP 连接失败")
	}
	return nil
}

func normalizeESReq(req *esUpsertReq) {
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

func esAuthType(t string) string {
	if t == "basic" {
		return "basic"
	}
	return "anonymous"
}

func strOf(v interface{}) string {
	s, _ := v.(string)
	return s
}

// pathEscape 转义索引/路径片段（允许英文数字 _ - + .，转义其余特殊字符）。
func pathEscape(s string) string {
	safe := strings.Builder{}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '+', r == '.', r == ',':
			safe.WriteRune(r)
		default:
			safe.WriteString(fmt.Sprintf("%%%02X", int(r)))
		}
	}
	return safe.String()
}
