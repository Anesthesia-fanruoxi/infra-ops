// Package redis 工具-Redis：连接配置的增删改查、key 浏览与值预览。
//
// 本工具是**只读**的：只会下发 SCAN / TYPE / TTL / GET 这类读命令，
// 不提供写入入口，故没有 MySQL 工具那套「会话读写模式」门禁。
package redis

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	"infra-ops/api/shared"
	"infra-ops/common/crypto"
	"infra-ops/common/redisclient"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store/repo"
)

const (
	defaultPort    = 6379
	connectTimeout = 10 * time.Second
	commandTimeout = 30 * time.Second
	// 单次请求的扫描预算：SCAN 只能一轮一轮推，匹配串偏的时候要连续很多轮才凑够，
	// 到点未扫完就返回 has_more，让使用者自己决定要不要继续（而不是把请求挂死）。
	scanTimeout = 2 * time.Minute
)

// Deps 处理器依赖（装配处一次性注入）。
type Deps struct {
	Repo     *repo.RedisRepo
	HostRepo *repo.HostRepo
	CredRepo *repo.CredentialRepo
	CryptoS  *crypto.Service
	SSHC     *sshx.Client
}

// Handler Redis 工具处理器。
type Handler struct {
	repo     *repo.RedisRepo
	hostRepo *repo.HostRepo
	credRepo *repo.CredentialRepo
	cryptoS  *crypto.Service
	sshC     *sshx.Client
	scans    *scanRegistry // 扫描游标会话：一次匹配分多次取，游标与去重集合存在这里
}

// NewHandler 创建 Redis 工具处理器。
func NewHandler(d Deps) *Handler {
	return &Handler{
		repo:     d.Repo,
		hostRepo: d.HostRepo,
		credRepo: d.CredRepo,
		cryptoS:  d.CryptoS,
		sshC:     d.SSHC,
		scans:    newScanRegistry(),
	}
}

// session 一次请求期间持有的 Redis 会话；走隧道时附带 SSH 连接与本地转发监听。
type session struct {
	cli     *redisclient.Client
	sshConn *ssh.Client
	ln      net.Listener
}

func (s *session) Close() {
	if s.cli != nil {
		_ = s.cli.Close()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.sshConn != nil {
		_ = s.sshConn.Close()
	}
}

// ---------------- 会话建立 ----------------

// password 解密连接密码。
func (h *Handler) password(conn *model.RedisConn) (string, error) {
	if len(conn.EncryptedSecret) == 0 {
		return "", nil
	}
	pw, err := h.cryptoS.Decrypt(conn.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("解密连接密码失败")
	}
	return string(pw), nil
}

// dial 建立 Redis 会话。选中跳板机时先建 SSH 隧道，再经本地转发端口连接。
func (h *Handler) dial(conn *model.RedisConn, db int) (*redisclient.Client, *session, error) {
	if conn.Port == 0 {
		conn.Port = defaultPort
	}
	target := net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
	addr := target
	s := &session{}

	if conn.SSHHostID > 0 {
		sshConn, ln, err := h.startTunnel(conn.SSHHostID, target)
		if err != nil {
			return nil, nil, err
		}
		s.sshConn, s.ln = sshConn, ln
		addr = ln.Addr().String()
	}

	pw, err := h.password(conn)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	if db < 0 {
		db = 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	cli, err := redisclient.Dial(ctx, redisclient.Options{
		Addr:     addr,
		Username: strings.TrimSpace(conn.Username),
		Password: pw,
		DB:       db,
		Timeout:  commandTimeout,
	})
	if err != nil {
		s.Close()
		return nil, nil, fmt.Errorf("连接失败: %w", err)
	}
	s.cli = cli
	return cli, s, nil
}

// startTunnel 复用「主机管理 + 凭据管理」建立 SSH 连接，并在本机随机端口做转发。
func (h *Handler) startTunnel(hostID int64, target string) (*ssh.Client, net.Listener, error) {
	host, err := h.hostRepo.GetByID(hostID)
	if err != nil || host == nil {
		return nil, nil, fmt.Errorf("跳板机不存在（host_id=%d）", hostID)
	}
	cred, err := h.credRepo.GetByID(host.CredentialID)
	if err != nil || cred == nil {
		return nil, nil, fmt.Errorf("跳板机 %s 未配置可用凭据", host.Name)
	}
	secret, err := h.cryptoS.Decrypt(cred.EncryptedSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("解密跳板机凭据失败")
	}
	dialCfg := sshx.DialConfig{
		Addr:     net.JoinHostPort(host.IP, strconv.Itoa(host.Port)),
		Username: cred.Username,
	}
	switch cred.Type {
	case "private_key":
		dialCfg.PrivateKey = secret
	default:
		dialCfg.Password = string(secret)
	}

	client, err := h.sshC.Dial(dialCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH 隧道建立失败（%s）: %w", host.Name, err)
	}
	ln, err := sshx.LocalForward(client, target)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("本地转发端口失败: %w", err)
	}
	return client, ln, nil
}

// ---------------- 配置 CRUD ----------------

type upsertReq struct {
	Name      string `json:"name" binding:"required"`
	Host      string `json:"host" binding:"required"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	DefaultDB int    `json:"default_db"`
	SSHHostID int64  `json:"ssh_host_id"`
	Remark    string `json:"remark"`
}

// List GET /api/redis
func (h *Handler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := shared.ParsePage(c)
	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 Redis 连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *Handler) getByID(c *gin.Context) (*model.RedisConn, bool) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return nil, false
	}
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "Redis 连接不存在")
		return nil, false
	}
	return conn, true
}

// buildConn 归一化请求为连接模型；密码为空时保留 nil（更新场景不覆盖原密码）。
func (h *Handler) buildConn(req *upsertReq) (*model.RedisConn, error) {
	req.Host = strings.TrimSpace(req.Host)
	if req.Port == 0 {
		req.Port = defaultPort
	}
	db := req.DefaultDB
	if db < 0 {
		db = 0
	}
	conn := &model.RedisConn{
		Name:      strings.TrimSpace(req.Name),
		Host:      req.Host,
		Port:      req.Port,
		Username:  strings.TrimSpace(req.Username),
		DefaultDB: db,
		SSHHostID: req.SSHHostID,
		Remark:    req.Remark,
	}
	if req.Password != "" {
		enc, err := h.cryptoS.Encrypt([]byte(req.Password))
		if err != nil {
			return nil, err
		}
		conn.EncryptedSecret = enc
	}
	return conn, nil
}

// Create POST /api/redis
func (h *Handler) Create(c *gin.Context) {
	var req upsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Host) == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	conn, err := h.buildConn(&req)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	id, err := h.repo.Create(conn)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建 Redis 连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

// Update PUT /api/redis/:id
func (h *Handler) Update(c *gin.Context) {
	if _, ok := h.getByID(c); !ok {
		return
	}
	var req upsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Host) == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	conn, err := h.buildConn(&req)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	if err := h.repo.Update(shared.ParseID(c), conn); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新 Redis 连接失败")
		return
	}
	resp.OK(c, nil)
}

// Delete DELETE /api/redis/:id
func (h *Handler) Delete(c *gin.Context) {
	id := shared.ParseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除 Redis 连接失败")
		return
	}
	// 顺带丢掉该连接的扫描会话，避免自增 ID 复用后继承上一份游标状态
	h.scans.dropByConn(id)
	resp.OK(c, nil)
}

// ---------------- 连通性与库列表 ----------------

// Ping GET /api/redis/:id/ping
func (h *Handler) Ping(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	cli, sess, err := h.dial(conn, conn.DefaultDB)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	defer sess.Close()

	start := time.Now()
	if _, err := cli.Do("PING"); err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	latency := time.Since(start).Milliseconds()

	version, mode := "", ""
	if info, err := cli.Do("INFO", "server"); err == nil {
		version, mode = serverInfo(info.Text())
	}
	size := int64(0)
	if n, err := cli.Do("DBSIZE"); err == nil {
		size, _ = strconv.ParseInt(n.Text(), 10, 64)
	}
	resp.OK(c, gin.H{
		"latency_ms": latency,
		"version":    version,
		"mode":       mode,
		"db":         conn.DefaultDB,
		"db_size":    size,
	})
}

// Databases GET /api/redis/:id/databases
// 有数据的库优先（INFO keyspace）；托管实例常禁用 CONFIG，取不到就回默认 0-15。
func (h *Handler) Databases(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	cli, sess, err := h.dial(conn, conn.DefaultDB)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "连接失败: "+err.Error())
		return
	}
	defer sess.Close()

	list := []model.RedisDBInfo{}
	if info, err := cli.Do("INFO", "keyspace"); err == nil {
		list = parseKeyspace(info.Text())
	}
	if len(list) == 0 {
		for i := 0; i < 16; i++ {
			list = append(list, model.RedisDBInfo{Index: i, Keys: -1, Expires: -1})
		}
	}
	resp.OK(c, gin.H{"list": list, "default_db": conn.DefaultDB})
}

// serverInfo 从 INFO server 原文里取版本与运行模式。
func serverInfo(text string) (version, mode string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "redis_version:"):
			version = strings.TrimPrefix(line, "redis_version:")
		case strings.HasPrefix(line, "redis_mode:"):
			mode = strings.TrimPrefix(line, "redis_mode:")
		}
	}
	return version, mode
}

// parseKeyspace 解析 INFO keyspace：`db0:keys=12,expires=3,avg_ttl=0`。
func parseKeyspace(text string) []model.RedisDBInfo {
	out := []model.RedisDBInfo{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "db") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 3 {
			continue
		}
		idx, err := strconv.Atoi(line[2:i])
		if err != nil {
			continue
		}
		item := model.RedisDBInfo{Index: idx, Keys: 0, Expires: 0}
		for _, kv := range strings.Split(line[i+1:], ",") {
			p := strings.SplitN(kv, "=", 2)
			if len(p) != 2 {
				continue
			}
			n, _ := strconv.ParseInt(p[1], 10, 64)
			switch p[0] {
			case "keys":
				item.Keys = n
			case "expires":
				item.Expires = n
			}
		}
		out = append(out, item)
	}
	return out
}
