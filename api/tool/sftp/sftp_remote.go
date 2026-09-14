package sftp

import (
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
)

// ---------------- 远程操作 ----------------

// ping GET /api/sftp/:id/ping 测试连接
func (h *Handler) Ping(c *gin.Context) {
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
func (h *Handler) Browse(c *gin.Context) {
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
func (h *Handler) Mkdir(c *gin.Context) {
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
func (h *Handler) Upload(c *gin.Context) {
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
func (h *Handler) Download(c *gin.Context) {
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

// remove DELETE /api/sftp/:id/delete?path=...
func (h *Handler) Remove(c *gin.Context) {
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
func (h *Handler) Rename(c *gin.Context) {
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
