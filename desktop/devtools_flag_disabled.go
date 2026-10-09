//go:build !devtools

package desktop

// 本构建未带 devtools 标签 → Wails 的 window.OpenDevTools() 是空函数，
// 按 F12 不会有任何反应，前端据此如实提示而不是让用户干等。
const devtoolsCompiled = false
