// 桌面模式（Wails v3）开发者工具桥：让 F12 能打开 DevTools。
//
// 为什么需要这一层（Windows 上的三重限制，每条都踩过）：
//
//  1. Wails 硬编码 settings.PutAreBrowserAcceleratorKeysEnabled(false)
//     （pkg/application/webview_window_windows.go），浏览器加速键被整体关掉，
//     所以 WebView2 自己的 F12 → DevTools 不再生效，按键被原生窗口吃掉、什么都不发生。
//
//  2. RegisterKeyBinding("f12") 在 Windows 上无效：windowKeyEvents 只有 darwin 与
//     linux 在投递（application_darwin.go / linux_cgo*.go），Windows 侧没有生产者。
//
//  3. window.OpenDevTools() 是否真有实现，由**构建 tag** 决定：
//
//     webview_window_windows_devtools.go     windows && (!production || devtools) → 有实现
//     webview_window_windows_production.go  windows && production && !devtools   → 空函数
//
//     只打 production 时 OpenDevTools 是空壳，main.go 里的 -debug 参数形同虚设
//     （不报错、不打日志、什么都不发生）。故构建必须同时打 devtools 标签，
//     见 script/build-desktop.ps1。
//
// 于是唯一可行路径是：前端 keydown 监听 F12 → 经 Call.ByName 调本服务 → window.OpenDevTools()。
// 前端侧见 template/static/app.js 的 F12 绑定。
//
// ⛔ 只能「打开」，不能「开/关切换」：Wails v3 beta.28 没有暴露 CloseDevTools
// （WebviewWindow 上只有 OpenDevTools），底层 OpenDevToolsWindow 是幂等的
// （已开则把焦点挪过去），拿它当 toggle 会变成「按多少下都关不掉」。
// 要关就点 DevTools 窗口右上角的 ×，或用 WebView2 的 Ctrl+Shift+I。
package desktop

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// DevToolsService 开发者工具服务（Wails 绑定服务）。
type DevToolsService struct {
	window application.Window

	mu     sync.Mutex
	opened bool // 是否已调用过 OpenDevTools，仅用于让前端按钮/提示显示正确状态
}

// NewDevToolsService 创建 DevTools 服务。
func NewDevToolsService(window application.Window) *DevToolsService {
	return &DevToolsService{window: window}
}

// Open 打开 DevTools（已打开时是聚焦到它，Wails 的 OpenDevToolsWindow 幂等）。
//
// 返回 false 表示**本构建不含 DevTools 能力**（未打 devtools 标签，Wails 那边是空函数）。
// 这时前端要给出明确提示，不能让用户按了没反应又不知道原因。
func (s *DevToolsService) Open() bool {
	if !DevToolsCompiled {
		return false
	}
	if s == nil || s.window == nil {
		return false
	}
	s.mu.Lock()
	s.opened = true
	s.mu.Unlock()
	s.window.OpenDevTools()
	return true
}

// Status 返回本进程是否已调用过 OpenDevTools。
// 注意它只反映「本进程打开过」，关掉 DevTools 窗口后这里仍是 true ——
// 上层只拿它做提示文案，不拿它当开关真值。
func (s *DevToolsService) Status() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}
