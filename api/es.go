// Package api 负责参数绑定校验、业务逻辑与对 store/common 的编排。
package api

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

	"github.com/gin-gonic/gin"

	"infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type esHandler struct {
	repo    *repo.ESRepo
	cryptoS *crypto.Service
}

func NewESHandler(repo *repo.ESRepo, cs *crypto.Service) *esHandler {
	return &esHandler{repo: repo, cryptoS: cs}
}

// ---------------- 配置 CRUD ----------------

type esUpsertReq struct {
	Name     string `json:"name" binding:"required"`
	URL      string `json:"url" binding:"required"`
	Insecure bool   `json:"insecure"`
	AuthType string `json:"auth_type"`
	Username string `json:"username"`
	Password string `json:"password"`
	Remark   string `json:"remark"`
}

func (h *esHandler) List(c *gin.Context) {
	page, pageSize := parsePage(c)
	items, total, err := h.repo.List(c.Query("keyword"), page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 ES 连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *esHandler) Create(c *gin.Context) {
	var req esUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeESReq(&req)
	var secret []byte
	if req.AuthType == "basic" && req.Password != "" {
		var err error
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	}
	id, err := h.repo.Create(&model.ESConn{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: esAuthType(req.AuthType), Username: req.Username,
		EncryptedSecret: secret, Remark: req.Remark,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建 ES 连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

func (h *esHandler) Update(c *gin.Context) {
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
	var req esUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	normalizeESReq(&req)

	var secret []byte
	if req.AuthType == "basic" && req.Password != "" {
		secret, err = h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
			return
		}
	} else if req.AuthType == "basic" {
		secret = existing.EncryptedSecret
	} // anonymous → 保持 nil 清空
	reg := &model.ESConn{
		Name: req.Name, URL: req.URL, Insecure: req.Insecure,
		AuthType: esAuthType(req.AuthType), Username: req.Username,
		EncryptedSecret: secret, Remark: req.Remark,
	}
	if err := h.repo.Update(id, reg); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新 ES 连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *esHandler) Delete(c *gin.Context) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除 ES 连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *esHandler) resolve(id int64) (*escClient, int, string) {
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		return nil, resp.CodeNotFound, "ES 连接不存在"
	}
	var password []byte
	if conn.AuthType == "basic" && len(conn.EncryptedSecret) > 0 {
		pw, err := h.cryptoS.Decrypt(conn.EncryptedSecret)
		if err != nil {
			return nil, resp.CodeInternal, "解密连接密码失败"
		}
		password = pw
	}
	client, err := newESCClient(conn.URL, conn.Insecure, conn.AuthType, conn.Username, string(password))
	if err != nil {
		return nil, resp.CodeBadRequest, "连接地址无效: "+err.Error()
	}
	return client, 0, ""
}

// ---------------- ES handlers ----------------

// Ping 测试连通性。
func (h *esHandler) Ping(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	start := time.Now()
	if err := client.ping(c.Request.Context()); err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": time.Since(start).Milliseconds()})
}

// Overview 集群概览：版本 + 健康。
func (h *esHandler) Overview(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	ov, err := client.overview(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取集群概览失败: "+err.Error())
		return
	}
	resp.OK(c, ov)
}

// Nodes 节点资源列表。
func (h *esHandler) Nodes(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	nodes, err := client.nodes(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取节点信息失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"list": nodes})
}

// Indices 索引列表。
func (h *esHandler) Indices(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	indices, err := client.indices(c.Request.Context())
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "获取索引列表失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"list": indices})
}

// CreateIndex 创建索引。
func (h *esHandler) CreateIndex(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		Index    string `json:"index" binding:"required"`
		Shards   *int   `json:"shards"`
		Replicas *int   `json:"replicas"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := client.createIndex(c.Request.Context(), req.Index, req.Shards, req.Replicas); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, nil)
}

// DeleteIndex 删除索引。
func (h *esHandler) DeleteIndex(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	index := strings.TrimSpace(c.Param("index"))
	if index == "" {
		resp.Fail(c, resp.CodeBadRequest, "缺少索引名")
		return
	}
	if err := client.deleteIndex(c.Request.Context(), index); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, nil)
}

// Search 文档查询。
func (h *esHandler) Search(c *gin.Context) {
	client, code, msg := h.resolve(parseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	var req struct {
		Index string          `json:"index" binding:"required"`
		From  int             `json:"from"`
		Size  int             `json:"size"`
		Query json.RawMessage `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if req.From < 0 {
		req.From = 0
	}
	if req.Size <= 0 || req.Size > 1000 {
		req.Size = 20
	}
	result, err := client.search(c.Request.Context(), req.Index, req.From, req.Size, req.Query)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, result)
}

// ---------------- ES HTTP 客户端 ----------------

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

func (c *escClient) overview(ctx context.Context) (*model.ESOverview, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取版本信息失败")
	}
	var info struct {
		ClusterName string `json:"cluster_name"`
		Version     struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("解析版本信息失败: %w", err)
	}

	status, body, err = c.do(ctx, http.MethodGet, "/_cluster/health?format=json", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取集群健康失败")
	}
	ov := &model.ESOverview{ClusterName: info.ClusterName, Version: info.Version.Number}
	var h struct {
		Status            string `json:"status"`
		TimedOut          bool   `json:"timed_out"`
		NumberOfNodes     int    `json:"number_of_nodes"`
		NumberOfDataNodes int    `json:"number_of_data_nodes"`
		ActiveShards      int    `json:"active_shards"`
		ActivePrimaries   int    `json:"active_primary_shards"`
		Relocating    int `json:"relocating_shards"`
		Initializing  int `json:"initializing_shards"`
		Unassigned    int `json:"unassigned_shards"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		return nil, fmt.Errorf("解析集群健康失败: %w", err)
	}
	ov.Status = h.Status
	ov.TimedOut = h.TimedOut
	ov.Nodes = h.NumberOfNodes
	ov.DataNodes = h.NumberOfDataNodes
	ov.ActiveShards = h.ActiveShards
	ov.ActivePrim = h.ActivePrimaries
	ov.Relocating = h.Relocating
	ov.Initializing = h.Initializing
	ov.Unassigned = h.Unassigned
	return ov, nil
}

// nodes 合并 _nodes（版本/角色）与 _nodes/stats（资源用量）。
func (c *escClient) nodes(ctx context.Context) ([]model.ESNode, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/_nodes?format=json", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取节点信息失败")
	}
	var info struct {
		Nodes map[string]struct {
			Name    string   `json:"name"`
			Version string   `json:"version"`
			Roles   []string `json:"roles"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("解析节点信息失败: %w", err)
	}

	status, body, err = c.do(ctx, http.MethodGet, "/_nodes/stats?format=json", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取节点统计失败")
	}
	var stats struct {
		Nodes map[string]struct {
			OS struct {
				CPU struct {
					Percent int `json:"percent"`
				} `json:"cpu"`
			} `json:"os"`
			JVM struct {
				Mem struct {
					HeapUsedPercent int `json:"heap_used_percent"`
				} `json:"mem"`
			} `json:"jvm"`
			FS struct {
				Total struct {
					TotalInBytes     int64 `json:"total_in_bytes"`
					AvailableInBytes int64 `json:"available_in_bytes"`
				} `json:"total"`
			} `json:"fs"`
			Indices struct {
				Docs struct {
					Count int64 `json:"count"`
				} `json:"docs"`
			} `json:"indices"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("解析节点统计失败: %w", err)
	}

	var nodes []model.ESNode
	for id, n := range info.Nodes {
		s, ok := stats.Nodes[id]
		if !ok {
			s = struct {
				OS struct {
					CPU struct {
						Percent int `json:"percent"`
					} `json:"cpu"`
				} `json:"os"`
				JVM struct {
					Mem struct {
						HeapUsedPercent int `json:"heap_used_percent"`
					} `json:"mem"`
				} `json:"jvm"`
				FS struct {
					Total struct {
						TotalInBytes     int64 `json:"total_in_bytes"`
						AvailableInBytes int64 `json:"available_in_bytes"`
					} `json:"total"`
				} `json:"fs"`
				Indices struct {
					Docs struct {
						Count int64 `json:"count"`
					} `json:"docs"`
				} `json:"indices"`
			}{}
		}
		nodes = append(nodes, model.ESNode{
			ID:            id,
			Name:          n.Name,
			Version:       n.Version,
			Roles:         n.Roles,
			CPUPercent:    s.OS.CPU.Percent,
			HeapPercent:   s.JVM.Mem.HeapUsedPercent,
			DiskTotal:     s.FS.Total.TotalInBytes,
			DiskAvailable: s.FS.Total.AvailableInBytes,
			DocsCount:     s.Indices.Docs.Count,
		})
	}
	return nodes, nil
}

func (c *escClient) indices(ctx context.Context) ([]model.ESIndex, error) {
	const path = "/_cat/indices?format=json&h=index,health,status,pri,rep,docs.count,docs.deleted,store.size&expand_wildcards=all"
	status, body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, body, "读取索引列表失败")
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("解析索引列表失败: %w", err)
	}
	indices := make([]model.ESIndex, 0, len(list))
	for _, m := range list {
		indices = append(indices, model.ESIndex{
			Index:        strOf(m["index"]),
			Health:       strOf(m["health"]),
			Status:       strOf(m["status"]),
			Primaries:    strOf(m["pri"]),
			Replicas:     strOf(m["rep"]),
			DocsCount:    strOf(m["docs.count"]),
			DocsDeleted:  strOf(m["docs.deleted"]),
			StoreSize:    strOf(m["store.size"]),
		})
	}
	return indices, nil
}

func (c *escClient) createIndex(ctx context.Context, name string, shards, replicas *int) error {
	bodyMap := map[string]interface{}{}
	if shards != nil || replicas != nil {
		settings := map[string]interface{}{}
		if shards != nil {
			settings["number_of_shards"] = *shards
		}
		if replicas != nil {
			settings["number_of_replicas"] = *replicas
		}
		bodyMap["settings"] = settings
	}
	var body []byte
	var err error
	if len(bodyMap) > 0 {
		body, err = json.Marshal(bodyMap)
		if err != nil {
			return fmt.Errorf("构造请求失败: %w", err)
		}
	}
	status, respBody, err := c.do(ctx, http.MethodPut, "/"+pathEscape(name), body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return esErr(status, respBody, "创建索引失败")
	}
	return nil
}

func (c *escClient) deleteIndex(ctx context.Context, name string) error {
	status, respBody, err := c.do(ctx, http.MethodDelete, "/"+pathEscape(name), nil)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("索引不存在(404)")
	}
	if status != http.StatusOK {
		return esErr(status, respBody, "删除索引失败")
	}
	return nil
}

func (c *escClient) search(ctx context.Context, index string, from, size int, rawQuery json.RawMessage) (*model.ESSearchResult, error) {
	q := json.RawMessage(`{"match_all":{}}`)
	if len(rawQuery) > 0 && string(rawQuery) != "null" {
		// 仅允许字面量对象，避免任意 JSON 注入风险
		if !bytes.HasPrefix(bytes.TrimSpace(rawQuery), []byte("{")) {
			return nil, fmt.Errorf("查询必须是 JSON 对象（如 {\"match_all\":{}}）")
		}
		q = rawQuery
	}
	bodyMap := map[string]interface{}{
		"from": from, "size": size, "query": q,
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("构造查询失败: %w", err)
	}
	status, respBody, err := c.do(ctx, http.MethodPost, "/"+pathEscape(index)+"/_search", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, esErr(status, respBody, "查询失败")
	}
	var raw struct {
		Took     int  `json:"took"`
		TimedOut bool `json:"timed_out"`
		Hits     struct {
			Total json.RawMessage `json:"total"`
			Hits  []struct {
				Index  string      `json:"_index"`
				ID     string      `json:"_id"`
				Score  interface{} `json:"_score"`
				Source interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("解析查询结果失败: %w", err)
	}
	result := &model.ESSearchResult{From: from, Size: size, Took: raw.Took, TimedOut: raw.TimedOut}
	// total 兼容 number 与 {value}
	var num json.Number
	if err := json.Unmarshal(raw.Hits.Total, &num); err == nil {
		result.Total, _ = num.Int64()
	} else {
		var tv struct {
			Value int64 `json:"value"`
		}
		_ = json.Unmarshal(raw.Hits.Total, &tv)
		result.Total = tv.Value
	}
	for _, h := range raw.Hits.Hits {
		result.Hits = append(result.Hits, model.ESSearchHit{
			Index: h.Index, ID: h.ID, Score: h.Score, Source: h.Source,
		})
	}
	if len(result.Hits) == 0 {
		result.Hits = []model.ESSearchHit{}
	}
	return result, nil
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