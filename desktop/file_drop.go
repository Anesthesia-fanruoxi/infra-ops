// desktop 模式（Wails v3）：窗口文件拖放 → 前端事件转发。
//
// Windows 链路（Wails v3 beta.28）：WebView2 原生捕获外部文件拖放 → 运行时 JS 把
// File 对象经 chrome.webview.postMessageWithAdditionalObjects 送回 Go（取出真实
// 路径）→ Wails 调 window._wails.handlePlatformFileDrop → 命中带 data-file-drop-target
// 的元素（closest 向上找）后把 WindowFilesDropped 交给 Go 监听。注意这一步只到 Go，
// 不会再回 JS —— 所以必须在这里转发成应用事件（infra:files-dropped），前端才能
// 拿到真实路径（wails-shim.js 封装为 window.__INFRA_DROP__ 供数据导入面板消费）。
package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// BindFileDrop 把窗口的文件拖放事件转发为前端事件 infra:files-dropped：
// {files: []string, elementId: string}。elementId 是命中 data-file-drop-target
// 元素的 id，前端据此确认投放位置（比如只有落在导入面板才接收）。
func BindFileDrop(app *application.App, window application.Window) {
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		files := event.Context().DroppedFiles()
		if len(files) == 0 {
			return
		}
		payload := map[string]any{"files": files}
		if d := event.Context().DropTargetDetails(); d != nil {
			payload["elementId"] = d.ElementID
		}
		app.Event.Emit("infra:files-dropped", payload)
	})
}
