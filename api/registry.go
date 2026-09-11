// Package api 负责参数绑定校验、业务逻辑与对 store/common 的编排。
package api

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

	"github.com/gin-gonic/gin"

	"infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type registryHandler struct {
	repo    *repo.RegistryRepo
	cryptoS *crypto.Service
}

func NewRegistryHandler(repo *repo.RegistryRepo, cs *crypto.Service) *registryHandler {
	return &registryHandler{repo: repo, cryptoS: cs}
}

// ---------------- 配置 CRUD ----------------

type registryUpsertReq struct {
	Name     string `json:"name" binding:"required"`
	URL      string `json:"url" binding:"required"`
	Insecure bool   `json:"insecure"`
	AuthType string `json:"auth_type"`
	Username string `json:"username"`
	Password string `json:"password"`
	Remark   string `json:"remark"`
}

func (h *registryHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := parsePage(c)
	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询镜像仓库连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *registryHandler) Create(c *gin.Context) {
	var req registryUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeRegistryReq(&req)
	var secret []byte
	var err error
	if req.AuthType == "basic" && req.Password != "" {
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	}
	reg := &model.Registry{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: authTypeOrDefault(req.AuthType),
		Username: req.Username, EncryptedSecret: secret, Remark: req.Remark,
	}
	id, err := h.repo.Create(reg)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建镜像仓库连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

func (h *registryHandler) Update(c *gin.Context) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	existing, err := h.repo.GetByID(id)
	if err != nil || existing == nil {
		resp.Fail(c, resp.CodeNotFound, "连接不存在")
		return
	}
	var req registryUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeRegistryReq(&req)

	var secret []byte
	if req.AuthType == "basic" && req.Password != "" {
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	}
	// 未修改密码时保留原密文；匿名切换为 basic 但未填密码时清空
	if secret == nil {
		if req.AuthType == "basic" {
			secret = existing.EncryptedSecret
		}
		// 改为匿名则继承 nil → 仅当从 basic 切到 anonymous 且原值存在时显式清空由 auth_type 列承担
		if req.AuthType != "basic" {
			secret = nil
		}
	}
	reg := &model.Registry{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: authTypeOrDefault(req.AuthType), Username: req.Username,
		EncryptedSecret: secret, Remark: req.Remark,
	}
	if err := h.repo.Update(id, reg); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新镜像仓库连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *registryHandler) Delete(c *gin.Context) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除镜像仓库连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *registryHandler) resolve(id int64) (*registryClient, int, string) {
	reg, err := h.repo.GetByID(id)
	if err != nil || reg == nil {
		return nil, resp.CodeNotFound, "镜像仓库连接不存在"
	}
	var password []byte
	if reg.AuthType == "basic" && len(reg.EncryptedSecret) > 0 {
		pw, err := h.cryptoS.Decrypt(reg.EncryptedSecret)
		if err != nil {
			return nil, resp.CodeInternal, "解密连接密码失败"
		}
		password = pw
	}
	client, err := newRegistryClient(reg.URL, reg.Insecure, reg.AuthType, reg.Username, string(password))
	if err != nil {
		return nil, resp.CodeBadRequest, "连接地址无效: " + err.Error()
	}
	return client, 0, ""
}

// ---------------- Docker Registry v2 API ----------------

// Ping 测试连通性。
func (h *registryHandler) Ping(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	start := time.Now()
	respHTTP, _, err := client.do(c.Request.Context(), http.MethodGet, "/v2/", nil, nil)
	latency := time.Since(start).Milliseconds()
	if respHTTP != nil && respHTTP.StatusCode == http.StatusUnauthorized {
		resp.Fail(c, resp.CodeUnauthorized, "认证失败，请检查用户名密码")
		return
	}
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": latency})
}

// Catalog 返回全部仓库（镜像名），后端全量拉取后按关键词过滤。
func (h *registryHandler) Catalog(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	repos, err := client.catalogAll(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取镜像列表失败: "+err.Error())
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	list := make([]model.RegistryImage, 0, len(repos))
	for _, name := range repos {
		if keyword != "" && !strings.Contains(strings.ToLower(name), keyword) {
			continue
		}
		list = append(list, model.RegistryImage{Name: name})
	}
	resp.OK(c, gin.H{"list": list, "total": len(list)})
}

// Tags 返回指定镜像的全部标签。
func (h *registryHandler) Tags(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少镜像名")
		return
	}
	tags, err := client.tagsAll(c.Request.Context(), name)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取标签列表失败: "+err.Error())
		return
	}
	// 常见时间推断：粗粒度排序用 server 返回顺序，这里直接返回原始顺序
	resp.OK(c, gin.H{"name": name, "tags": tags})
}

// Manifest 返回镜像某引用（tag 或 digest）的 manifest 详情。
func (h *registryHandler) Manifest(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	ref := strings.TrimSpace(c.Query("reference"))
	if name == "" || ref == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少镜像名或引用")
		return
	}
	info, err := client.manifest(c.Request.Context(), name, ref)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取镜像信息失败: "+err.Error())
		return
	}
	resp.OK(c, info)
}

// DeleteRepo 删除镜像（reference 为 tag 或 digest）：先解析 digest，再删除对应 manifest。
func (h *registryHandler) DeleteRepo(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	name := strings.TrimSpace(c.Query("name"))
	ref := strings.TrimSpace(c.Query("reference"))
	if name == "" || ref == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少镜像名或引用")
		return
	}
	info, err := client.manifest(c.Request.Context(), name, ref)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "解析镜像引用失败: "+err.Error())
		return
	}
	if err := client.deleteManifest(c.Request.Context(), name, info.Digest, info.MediaType); err != nil {
		resp.Fail(c, resp.CodeInternal, "删除失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{
		"digest": info.Digest,
		"tip":    "镜像已标记删除，需在仓库侧执行垃圾回收(registry GC)才能真正释放存储空间",
	})
}

// ---------------- Registry v2 HTTP 客户端 ----------------

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
		// 自定义 TLS 校验放在 DialTLSContext 以同时覆盖基础与安全两种形态
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

func (rc *registryClient) catalogAll(ctx context.Context) ([]string, error) {
	var all []string
	last := ""
	for i := 0; i < 20; i++ {
		p := "/v2/_catalog?n=500"
		if last != "" {
			p += "&last=" + url.QueryEscape(last)
		}
		if _, body, err := rc.do(ctx, http.MethodGet, p, nil, nil); err != nil {
			return nil, err
		} else {
			var out struct {
				Repositories []string `json:"repositories"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, fmt.Errorf("解析镜像列表失败: %w", err)
			}
			if len(out.Repositories) == 0 {
				break
			}
			all = append(all, out.Repositories...)
			last = out.Repositories[len(out.Repositories)-1]
			if len(out.Repositories) < 500 {
				break
			}
		}
	}
	return all, nil
}

func (rc *registryClient) tagsAll(ctx context.Context, name string) ([]string, error) {
	var all []string
	last := ""
	for i := 0; i < 20; i++ {
		p := "/v2/" + name + "/tags/list?n=500"
		if last != "" {
			p += "&last=" + url.QueryEscape(last)
		}
		if _, body, err := rc.do(ctx, http.MethodGet, p, nil, nil); err != nil {
			return nil, err
		} else {
			var out struct {
				Tags []string `json:"tags"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, fmt.Errorf("解析标签列表失败: %w", err)
			}
			if len(out.Tags) == 0 {
				break
			}
			all = append(all, out.Tags...)
			last = out.Tags[len(out.Tags)-1]
			if len(out.Tags) < 500 {
				break
			}
		}
	}
	return all, nil
}

var manifestAccept = []string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.v1+prettyjws",
}

type registryManifestInfo struct {
	Reference string             `json:"reference"`
	Digest    string             `json:"digest"`
	MediaType string             `json:"media_type"`
	Size      int64              `json:"size"`
	Created   string             `json:"created,omitempty"`
	IsList    bool               `json:"is_list"`
	Platforms []registryPlatform `json:"platforms"`
	Config    registryLayer      `json:"config,omitempty"`
	Layers    []registryLayer    `json:"layers"`
}

type registryLayer struct {
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

type registryPlatform struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Variant   string `json:"variant,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

func (rc *registryClient) manifest(ctx context.Context, name, reference string) (*registryManifestInfo, error) {
	h := http.Header{}
	h.Set("Accept", strings.Join(manifestAccept, ", "))
	resp, body, err := rc.do(ctx, http.MethodGet, "/v2/"+name+"/manifests/"+reference, h, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("镜像 %s:%s 不存在", name, reference)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("查询失败(%d)", resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	mediaType := resp.Header.Get("Content-Type")
	if digest == "" {
		digest = ""
	}
	info := &registryManifestInfo{
		Reference: reference, Digest: digest, MediaType: mediaType,
		Size: int64(len(body)),
	}

	var decode struct {
		MediaType string             `json:"mediaType"`
		Manifests []registryPlatform `json:"manifests"`
		Config    struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
			MediaType    string `json:"mediaType"`
			Size         int64  `json:"size"`
			Digest       string `json:"digest"`
		} `json:"config"`
		Layers   []registryLayer   `json:"layers"`
		Platform *registryPlatform `json:"platform"`
	}
	_ = json.Unmarshal(body, &decode)
	if decode.MediaType != "" {
		mediaType = decode.MediaType
	}
	isList := strings.Contains(mediaType, "manifest.list") || strings.Contains(mediaType, "image.index")
	info.IsList = isList
	info.MediaType = mediaType
	info.Layers = decode.Layers
	if decode.Config.Digest != "" {
		info.Config = registryLayer{MediaType: decode.Config.MediaType, Size: decode.Config.Size, Digest: decode.Config.Digest}
	}
	if isList {
		info.Platforms = decode.Manifests
	} else if len(decode.Manifests) == 0 {
		// 单架构镜像：从 config 提取平台，便于前端统一展示
		if decode.Platform != nil {
			info.Platforms = []registryPlatform{*decode.Platform}
		} else if decode.Config.OS != "" || decode.Config.Architecture != "" {
			info.Platforms = []registryPlatform{{OS: decode.Config.OS, Arch: decode.Config.Architecture}}
		}
		// 读取 config blob 获取镜像创建时间（尽力而为，失败不影响详情展示）
		if decode.Config.Digest != "" {
			if cResp, cBody, cErr := rc.do(ctx, http.MethodGet, "/v2/"+name+"/blobs/"+decode.Config.Digest, nil, nil); cErr == nil && cResp.StatusCode == http.StatusOK {
				var cfg struct {
					Created string `json:"created"`
				}
				if json.Unmarshal(cBody, &cfg) == nil && cfg.Created != "" {
					info.Created = cfg.Created
				}
			}
		}
	}
	return info, nil
}

func (rc *registryClient) deleteManifest(ctx context.Context, name, digest, mediaType string) error {
	if digest == "" {
		return fmt.Errorf("缺少 manifest digest，无法删除")
	}
	h := http.Header{}
	h.Set("Accept", strings.Join(manifestAccept, ", "))
	if mediaType != "" {
		h.Set("Content-Type", mediaType)
	}
	resp, body, err := rc.do(ctx, http.MethodDelete, "/v2/"+name+"/manifests/"+digest, h, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("镜像已不存在")
	}
	msg := strings.TrimSpace(string(body))
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("仓库不允许删除操作(%d)——请确认 registry 已开启 delete 功能: %s", resp.StatusCode, msg)
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("删除失败(%d): %s", resp.StatusCode, msg)
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
