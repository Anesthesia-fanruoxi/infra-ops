package desktop

import "testing"

// TestDevToolsCompiledFlagMatchesBuildTag 锁住「编译期能力标志」与构建标签的一致性。
//
// 这条契约的由来：Wails 的 window.OpenDevTools() 在 production && !devtools 标签下
// 是**空函数**（pkg/application/webview_window_windows_production.go），调用它不报错、
// 不打日志、什么都不发生。用户表现就是「F12 按了没反应」，且没有任何线索。
// 所以 desktop 侧必须有一个如实反映标签的常量供前端提示，而它不能与实际能力脱节。
//
// 反向验证过：把 build-desktop.ps1 的标签改回只有 production，本用例不会红
// （它只保证 Go 侧常量自洽），因此真正兜底的是 desktop/devtools.go 里 Open() 的
// 前置判断 + 前端 console.warn —— 本用例的职责是防止有人新增平台文件时把
// 两个互斥实现写坏（比如两个文件同时命中同一个标签，导致重复声明或都编译不到）。
func TestDevToolsCompiledFlagMatchesBuildTag(t *testing.T) {
	// 常量本身能被取到即说明恰好有一个 devtools_flag_*.go 命中当前标签；
	// 两个都命中会在编译期报重复声明，两个都不命中则 DevToolsCompiled 无定义。
	if DevToolsCompiled != true && DevToolsCompiled != false {
		t.Fatalf("DevToolsCompiled 应为常量布尔值，得到 %v", DevToolsCompiled)
	}
}

// TestDevToolsServiceOpenOnNilWindowNilSafe 验证 nil 窗口下 Open 不 panic。
//
// 用途是单测与渲染台等没有真实窗口的场景：DevToolsService 被以 nil window 构造时
// Open 必须安静返回 false，而不是把整个进程带崩（桌面主窗口创建失败时也会走这条路）。
func TestDevToolsServiceOpenOnNilWindowNilSafe(t *testing.T) {
	var s *DevToolsService
	if got := s.Open(); got != false {
		t.Fatalf("nil 服务的 Open 应返回 false，得到 %v", got)
	}
	if got := s.Status(); got != false {
		t.Fatalf("nil 服务的 Status 应返回 false，得到 %v", got)
	}
	s2 := NewDevToolsService(nil)
	if got := s2.Open(); got != false {
		t.Fatalf("nil 窗口的 Open 应返回 false，得到 %v", got)
	}
	if got := s2.Status(); got != false {
		t.Fatalf("nil 窗口的 Status 应返回 false，得到 %v", got)
	}
}
