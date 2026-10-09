//go:build devtools

package desktop

// 本构建带 devtools 标签 → window.OpenDevTools() 有真实实现（见 devtools.go）。
const devtoolsCompiled = true
