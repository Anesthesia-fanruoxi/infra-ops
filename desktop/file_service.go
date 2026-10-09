// 桌面模式（Wails v3）文件服务：原生文件对话框 + 本地直传/直落盘。
//
// 背景：大文件经 webview 内的 multipart/blob 流量会由资产服务器整段缓冲在内存
// （wailsapp/wails#2847 同类限制），桌面端统一改为：
//   1. Go 侧弹原生打开/保存对话框取得本地路径（webview 不搬运字节流）；
//   2. Go 侧流式读写（SFTP 上传下载、套件资产入库）。
// 前端经 Call.ByName('infra-ops/desktop.FileService.*') 调用；浏览器调试环境
// （window.__INFRA_DESKTOP__ 为假）仍走原 HTTP FormData 路径。
package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"infra-ops/api/stack"
	"infra-ops/api/tool/sftp"
	"infra-ops/model"
)

// FileService 桌面端文件服务（Wails 绑定服务）。
type FileService struct {
	app    *application.App
	window application.Window
	sftpH  *sftp.Handler
}

// NewFileService 创建文件服务；sftpH 复用 SFTP 模块的连接层与凭据解密。
func NewFileService(app *application.App, window application.Window, sftpH *sftp.Handler) *FileService {
	return &FileService{app: app, window: window, sftpH: sftpH}
}

// SFTPTransferResult 单个文件的直传结果（上传时 Errors 为空表示成功）。
type SFTPTransferResult struct {
	Name  string `json:"name"`  // 本地文件名
	Path  string `json:"path"`  // 远程全路径（成功后）
	Size  int64  `json:"size"`  // 字节数
	Error string `json:"error"` // 单项失败原因（成功为空串）
}

// isDialogCancelled 判断原生对话框是否被用户取消（wails cfd 层返回 "cancelled by user"）。
// 取消不是错误：调用方按「无选择」静默处理。
func isDialogCancelled(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "cancel")
}

// PickFiles 弹出原生多选文件对话框，返回选中的本地绝对路径列表；取消返回空列表。
func (s *FileService) PickFiles(title string) ([]string, error) {
	if strings.TrimSpace(title) == "" {
		title = "选择文件"
	}
	files, err := s.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AttachToWindow(s.window).
		PromptForMultipleSelection()
	if isDialogCancelled(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if files == nil {
		files = []string{}
	}
	return files, nil
}

// UploadToSFTP 把本地文件逐个直传到远程目录（自动建目录），单项失败不中断其余文件。
// 校验失败（连接不存在等）以整体 error 返回；单文件错误写入结果项的 Error 字段。
func (s *FileService) UploadToSFTP(connID int64, localPaths []string, remoteDir string) ([]SFTPTransferResult, error) {
	if len(localPaths) == 0 {
		return nil, fmt.Errorf("未选择文件")
	}
	results := make([]SFTPTransferResult, 0, len(localPaths))
	for _, lp := range localPaths {
		r := SFTPTransferResult{Name: filepath.Base(lp)}
		path, size, err := s.sftpH.UploadLocalFile(connID, lp, remoteDir)
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Path, r.Size = path, size
		}
		results = append(results, r)
	}
	return results, nil
}

// DownloadFromSFTP 下载远程文件：先预检远程路径（避免白选保存位置），
// 弹原生保存对话框后流式落盘。返回保存的本地路径；用户取消返回空串。
func (s *FileService) DownloadFromSFTP(connID int64, remotePath string) (string, error) {
	fi, err := s.sftpH.StatRemote(connID, remotePath)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("目标为目录，无法下载")
	}
	saveDlg := s.app.Dialog.SaveFile()
	saveDlg.SetOptions(&application.SaveFileDialogOptions{
		Title:                "保存到本地",
		Filename:             filepath.Base(remotePath),
		CanCreateDirectories: true,
		Window:               s.window,
	})
	saved, err := saveDlg.PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if saved == "" {
		return "", nil
	}
	if _, err := s.sftpH.DownloadToLocalFile(connID, remotePath, saved); err != nil {
		return "", err
	}
	return saved, nil
}

// PickSQLFile 弹出原生打开对话框选择要导入的 SQL 文件；取消返回空串。
// 单文件即可——一次导入多份会把「出错时到底哪份坏了」变得难查。
func (s *FileService) PickSQLFile(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "选择 SQL 文件"
	}
	path, err := s.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("SQL 文件", "*.sql").
		AddFilter("文本文件", "*.txt").
		AddFilter("所有文件", "*.*").
		AttachToWindow(s.window).
		PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	return path, err
}

// PickBackupFile 弹出原生打开对话框选择备份文件（.iopsbak）；取消返回空串。
func (s *FileService) PickBackupFile(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "选择备份文件"
	}
	path, err := s.app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("infra-ops 备份", "*.iopsbak").
		AddFilter("所有文件", "*.*").
		AttachToWindow(s.window).
		PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	return path, err
}

// SaveFile 通用保存对话框：返回目标绝对路径；用户取消返回空串。
//
// 导出走「Go 侧边算边写」而不是把文件吐回前端，所以必须先在这里拿到路径——
// 大文件经 webview 缓冲会吃掉整个窗口的内存（见文件头说明）。
func (s *FileService) SaveFile(title, defaultName, filterName, filterPattern string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "保存文件"
	}
	filters := []application.FileFilter{}
	if strings.TrimSpace(filterPattern) != "" {
		name := filterName
		if strings.TrimSpace(name) == "" {
			name = "文件"
		}
		filters = append(filters, application.FileFilter{DisplayName: name, Pattern: filterPattern})
	}
	filters = append(filters, application.FileFilter{DisplayName: "所有文件", Pattern: "*.*"})

	dlg := s.app.Dialog.SaveFile()
	dlg.SetOptions(&application.SaveFileDialogOptions{
		Title:                title,
		Filename:             defaultName,
		CanCreateDirectories: true,
		Filters:              filters,
		Window:               s.window,
	})
	saved, err := dlg.PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	return saved, err
}

// PickAssetFile 弹出原生文件对话框选择套件资产文件（按期望文件名扩展名过滤），
// 返回本地路径；取消返回空串。文件名与套件声明的匹配校验在 StoreAsset 中执行。
func (s *FileService) PickAssetFile(expectedName string) (string, error) {
	dlg := s.app.Dialog.OpenFile().
		SetTitle("选择资产文件（期望 " + expectedName + "）").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AttachToWindow(s.window)
	if ext := filepath.Ext(expectedName); ext != "" {
		dlg = dlg.AddFilter(strings.TrimPrefix(ext, ".")+" 文件", "*"+ext)
	}
	dlg = dlg.AddFilter("所有文件", "*.*")
	path, err := dlg.PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	return path, err
}

// StoreAsset 把本地文件入库为套件部署资产（校验命名规则、大小上限与文件名匹配套件声明）。
func (s *FileService) StoreAsset(assetKey, version, fileName, localPath string) (*model.StackAsset, error) {
	return stack.StoreLocalAssetFile(assetKey, version, fileName, localPath)
}
