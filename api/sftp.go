package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store/repo"
)

type sftpHandler struct {
	repo    *repo.SFTPRepo
	cryptoS *crypto.Service
}

func NewSFTPHandler(r *repo.SFTPRepo, cs *crypto.Service) *sftpHandler {
	return &sftpHandler{repo: r, cryptoS: cs}
}

// ---------------- 配置 CRUD ----------------

type sftpUpsertReq struct {
	Name     string `json:"name" binding:"required"`
	Host     string `json:"host" binding:"required"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Key      string `json:"key"`
	Remark   string `json:"remark"`
}

// List GET /api/sftp
func (h *sftpHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	page, pageSize := parsePage(c)
	items, total, err := h.repo.List(keyword, page, pageSize)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 SFTP 连接失败")
		return
	}
	resp.OK(c, resp.PageData{List: items, Total: total, Page: page, PageSize: pageSize})
}

func (h *sftpHandler) getByID(c *gin.Context) (*model.SFTPConn, bool) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return nil, false
	}
	conn, err := h.repo.GetByID(id)
	if err != nil || conn == nil {
		resp.Fail(c, resp.CodeNotFound, "SFTP 连接不存在")
		return nil, false
	}
	return conn, true
}

func encryptIfNeeded(h *sftpHandler, secret, key string) (secB, keyB []byte, err error) {
	if secret != "" {
		secB, err = h.cryptoS.Encrypt([]byte(secret))
		if err != nil {
			return nil, nil, err
		}
	}
	if key != "" {
		keyB, err = h.cryptoS.Encrypt([]byte(key))
		if err != nil {
			return nil, nil, err
		}
	}
	return secB, keyB, nil
}

func (h *sftpHandler) Create(c *gin.Context) {
	var req sftpUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	secB, keyB, err := encryptIfNeeded(h, req.Password, req.Key)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	conn := &model.SFTPConn{
		Name: req.Name, Host: req.Host, Port: req.Port, Username: req.Username,
		EncryptedSecret: secB, EncryptedKey: keyB, Remark: req.Remark,
	}
	id, err := h.repo.Create(conn)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "创建 SFTP 连接失败")
		return
	}
	resp.OK(c, gin.H{"id": id})
}

func (h *sftpHandler) Update(c *gin.Context) {
	_, ok := h.getByID(c)
	if !ok {
		return
	}
	id := parseID(c)
	var req sftpUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		resp.Fail(c, resp.CodeBadRequest, "主机地址不能为空")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	secB, keyB, err := encryptIfNeeded(h, req.Password, req.Key)
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "加密失败")
		return
	}
	conn := &model.SFTPConn{
		Name: req.Name, Host: req.Host, Port: req.Port, Username: req.Username,
		EncryptedSecret: secB, EncryptedKey: keyB, Remark: req.Remark,
	}
	if err := h.repo.Update(id, conn); err != nil {
		if strings.Contains(err.Error(), "duplicate name") {
			resp.Fail(c, resp.CodeConflict, "连接名称已存在")
			return
		}
		resp.ErrHTTP(c, 500, resp.CodeInternal, "更新 SFTP 连接失败")
		return
	}
	resp.OK(c, nil)
}

func (h *sftpHandler) Delete(c *gin.Context) {
	id := parseID(c)
	if id == 0 {
		resp.Fail(c, resp.CodeBadRequest, "无效的连接 ID")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "删除 SFTP 连接失败")
		return
	}
	resp.OK(c, nil)
}

// ---------------- 远程操作 ----------------

// dial 建立一次 SFTP 连接（调用方负责 Close）。
func (h *sftpHandler) dial(conn *model.SFTPConn) (*sftp.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            conn.Username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	var auths []ssh.AuthMethod
	if len(conn.EncryptedKey) > 0 {
		key, err := h.cryptoS.Decrypt(conn.EncryptedKey)
		if err == nil {
			if signer, err := ssh.ParsePrivateKey(key); err == nil {
				auths = append(auths, ssh.PublicKeys(signer))
			}
		}
	}
	if len(conn.EncryptedSecret) > 0 {
		if pwd, err := h.cryptoS.Decrypt(conn.EncryptedSecret); err == nil {
			auths = append(auths, ssh.Password(string(pwd)))
		}
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("未配置认证凭据（密码或私钥）")
	}
	cfg.Auth = auths

	addr := net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
	sshConn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	client, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("初始化 SFTP 会话失败: %w", err)
	}
	return client, nil
}

// ping GET /api/sftp/:id/ping 测试连接
func (h *sftpHandler) Ping(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	start := time.Now()
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()
	if _, err := client.Getwd(); err != nil {
		resp.Fail(c, resp.CodeInternal, "连接成功但无法访问根目录: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"latency_ms": time.Since(start).Milliseconds()})
}

func cleanRemotePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func dirParent(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	t := strings.TrimRight(p, "/")
	idx := strings.LastIndex(t, "/")
	if idx <= 0 {
		return "/"
	}
	return t[:idx]
}

// browse GET /api/sftp/:id/browse?path=/
func (h *sftpHandler) Browse(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()

	path := cleanRemotePath(c.Query("path"))
	entries, err := client.ReadDir(path)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "读取目录失败: "+err.Error())
		return
	}

	// 目录优先，其余按名称排序
	result := make([]model.SFTPEntry, 0, len(entries))
	for _, fi := range entries {
		ep := path + "/" + fi.Name()
		if path == "/" {
			ep = "/" + fi.Name()
		}
		result = append(result, model.SFTPEntry{
			Name: fi.Name(), Path: ep, Size: fi.Size(),
			ModTime: fi.ModTime().Format("2006-01-02 15:04:05"),
			IsDir:   fi.IsDir(), Mode: fi.Mode().String(),
		})
	}
	resp.OK(c, model.SFTPBrowse{Current: path, Parent: dirParent(path), Entries: result})
}

// mkdir POST /api/sftp/:id/mkdir {path}
func (h *sftpHandler) Mkdir(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()
	if err := client.MkdirAll(cleanRemotePath(req.Path)); err != nil {
		resp.Fail(c, resp.CodeInternal, "创建目录失败: "+err.Error())
		return
	}
	resp.OK(c, nil)
}

// upload POST /api/sftp/:id/upload (multipart: path + file)
func (h *sftpHandler) Upload(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	dir := cleanRemotePath(c.PostForm("path"))
	file, hdr, err := c.Request.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "缺少上传文件")
		return
	}
	defer file.Close()

	filename := filepath.Base(hdr.Filename)
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()
	if err := client.MkdirAll(dir); err != nil {
		resp.Fail(c, resp.CodeInternal, "创建目标目录失败: "+err.Error())
		return
	}
	dst, err := client.Create(dir + "/" + filename)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "创建远程文件失败: "+err.Error())
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		resp.Fail(c, resp.CodeInternal, "上传失败: "+err.Error())
		return
	}
	resp.OK(c, gin.H{"path": dir + "/" + filename, "size": hdr.Size})
}

// download GET /api/sftp/:id/download?path=/a/b.txt
func (h *sftpHandler) Download(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	remote := cleanRemotePath(c.Query("path"))
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()

	src, err := client.Open(remote)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "打开远程文件失败: "+err.Error())
		return
	}
	defer src.Close()
	fi, _ := src.Stat()
	if fi != nil && fi.IsDir() {
		resp.Fail(c, resp.CodeBadRequest, "目标为目录，无法下载")
		return
	}

	filename := filepath.Base(remote)
	encoded := url.PathEscape(filename)
	c.Header("Content-Disposition", "attachment; filename=\"download\"; filename*=UTF-8''"+encoded)
	c.Header("Content-Type", "application/octet-stream")
	if fi != nil {
		c.Header("Content-Length", strconv.FormatInt(fi.Size(), 10))
	}
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(c.Writer, src); err != nil {
		// 响应已开始，无法用 resp 返回错误
		c.Error(err)
	}
}

func removeRecursive(client *sftp.Client, p string) error {
	entries, err := client.ReadDir(p)
	if err != nil {
		return fmt.Errorf("读取目录 %s: %v", p, err)
	}
	for _, e := range entries {
		ep := p + "/" + e.Name()
		if p == "/" {
			ep = "/" + e.Name()
		}
		if e.IsDir() {
			if err := removeRecursive(client, ep); err != nil {
				return err
			}
		} else {
			if err := client.Remove(ep); err != nil {
				return err
			}
		}
	}
	return client.RemoveDirectory(p)
}

// remove DELETE /api/sftp/:id/delete?path=...
func (h *sftpHandler) Remove(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	remote := cleanRemotePath(c.Query("path"))
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()

	fi, err := client.Stat(remote)
	if err != nil {
		resp.Fail(c, resp.CodeNotFound, "目标不存在: "+err.Error())
		return
	}
	if fi.IsDir() {
		if err := removeRecursive(client, remote); err != nil {
			resp.Fail(c, resp.CodeInternal, "删除目录失败: "+err.Error())
			return
		}
	} else {
		if err := client.Remove(remote); err != nil {
			resp.Fail(c, resp.CodeInternal, "删除文件失败: "+err.Error())
			return
		}
	}
	resp.OK(c, nil)
}

// rename POST /api/sftp/:id/rename {from,to}
func (h *sftpHandler) Rename(c *gin.Context) {
	conn, ok := h.getByID(c)
	if !ok {
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误")
		return
	}
	if strings.TrimSpace(req.From) == "" || strings.TrimSpace(req.To) == "" {
		resp.Fail(c, resp.CodeBadRequest, "from/to 不能为空")
		return
	}
	client, err := h.dial(conn)
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	defer client.Close()
	to := req.To
	if !strings.Contains(to, "/") {
		to = dirParent(cleanRemotePath(req.From)) + "/" + to
	}
	if err := client.Rename(cleanRemotePath(req.From), cleanRemotePath(to)); err != nil {
		resp.Fail(c, resp.CodeInternal, "重命名失败: "+err.Error())
		return
	}
	resp.OK(c, nil)
}