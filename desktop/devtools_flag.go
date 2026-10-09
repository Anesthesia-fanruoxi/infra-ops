// 桌面模式（Wails v3）开发者工具能力标志：编译期如实反映本构建是否带 DevTools。
//
// 为什么要单独一个文件：Wails 的 window.OpenDevTools() 在 production && !devtools 标签下
// 是**空函数**（pkg/application/webview_window_windows_production.go），调用它
// 不报错、不打日志、什么都不发生。若前端不区分，用户按 F12 没反应却无从判断原因。
// 这里用两个互斥的空实现把「构建标签」翻译成一个布尔常量：
//   - 带 devtools 标签 → const DevToolsCompiled = true
//   - 未带           → const DevToolsCompiled = false
//
// 判定与 pkg/application 的 tag 完全一致（照抄其条件，不要自己另发明）：
//
//	pkg/application/webview_window_windows_devtools.go    windows && (!production || devtools)
//	pkg/application/webview_window_windows_production.go windows && production && !devtools
//
// 故 devtools.go 只在 devtools 标签下有实现。
package desktop

// devtoolsEnabled 与 devtools_disabled.go 用互斥的 tag 提供这两个常量。
const (
	// DevToolsCompiled 表示本构建的 window.OpenDevTools() 有真实实现。
	DevToolsCompiled = devtoolsCompiled
)
