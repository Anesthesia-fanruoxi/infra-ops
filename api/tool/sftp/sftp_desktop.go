// 桌面端（Wails 绑定）直传/直落盘：与 HTTP 路由共用 dial / cleanRemotePath，
// 但字节流不经 webview（multipart 大文件会在资产服务器内存缓冲），
// 由 desktop/file_service.go 在原生文件对话框取得本地路径后直接调用。
package sftp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// UploadLocalFile 从本地路径流式上传到远程目录（自动建目录），返回远程全路径与字节数。
func (h *Handler) UploadLocalFile(connID int64, localPath, remoteDir string) (string, int64, error) {
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		return "", 0, fmt.Errorf("SFTP 连接不存在")
	}
	fi, err := os.Stat(localPath)
	if err != nil {
		return "", 0, fmt.Errorf("读取本地文件失败: %w", err)
	}
	if fi.IsDir() {
		return "", 0, fmt.Errorf("不支持上传目录")
	}
	src, err := os.Open(localPath)
	if err != nil {
		return "", 0, fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer src.Close()

	client, err := h.dial(conn)
	if err != nil {
		return "", 0, err
	}
	defer client.Close()
	dir := cleanRemotePath(remoteDir)
	if err := client.MkdirAll(dir); err != nil {
		return "", 0, fmt.Errorf("创建目标目录失败: %w", err)
	}
	remote := dir + "/" + filepath.Base(localPath)
	if dir == "/" {
		remote = "/" + filepath.Base(localPath)
	}
	dst, err := client.Create(remote)
	if err != nil {
		return "", 0, fmt.Errorf("创建远程文件失败: %w", err)
	}
	defer dst.Close()
	n, err := io.Copy(dst, src)
	if err != nil {
		return "", 0, fmt.Errorf("上传失败: %w", err)
	}
	return remote, n, nil
}

// StatRemote 探测远程路径元信息（桌面端下载前预检，避免白选保存位置）。
func (h *Handler) StatRemote(connID int64, remotePath string) (os.FileInfo, error) {
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		return nil, fmt.Errorf("SFTP 连接不存在")
	}
	client, err := h.dial(conn)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	fi, err := client.Stat(cleanRemotePath(remotePath))
	if err != nil {
		return nil, fmt.Errorf("远程文件不可访问: %w", err)
	}
	return fi, nil
}

// DownloadToLocalFile 把远程文件流式写入本地路径（覆盖已存在文件），返回写入字节数。
func (h *Handler) DownloadToLocalFile(connID int64, remotePath, localPath string) (int64, error) {
	conn, err := h.repo.GetByID(connID)
	if err != nil || conn == nil {
		return 0, fmt.Errorf("SFTP 连接不存在")
	}
	client, err := h.dial(conn)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	// 先打开远程文件（失败时不创建本地文件）
	src, err := client.Open(cleanRemotePath(remotePath))
	if err != nil {
		return 0, fmt.Errorf("打开远程文件失败: %w", err)
	}
	defer src.Close()
	if fi, _ := src.Stat(); fi != nil && fi.IsDir() {
		return 0, fmt.Errorf("目标为目录，无法下载")
	}
	dst, err := os.Create(localPath)
	if err != nil {
		return 0, fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer dst.Close()
	n, err := io.Copy(dst, src)
	if err != nil {
		return 0, fmt.Errorf("下载失败: %w", err)
	}
	return n, nil
}
