# infra-ops 项目约定

## 项目形态

- Wails v3 桌面应用（Windows 优先）：**不监听任何端口**，Gin 引擎在进程内直接挂为窗口资产服务器（`main.go` → `application.AssetOptions.Handler`）
- 数据目录：启动时 `os.Chdir` 到用户数据目录（Windows: `%APPDATA%\infra-ops`），全部相对路径（`data/infra-ops.db`、`data/assets/...`）随之落位
- 前端无 npm：`template/static/` 由 `go:embed` 打包；**改前端后必须重新 `go build` 才生效**
- SSE：`wails-shim.js` 用 Wails 事件桥覆盖 `window.EventSource`（桌面桥在 `desktop/sse_bridge.go`）
- 无认证体系：单机桌面形态，接口全部免登录、无 token；审计中间件（`common/middleware/audit.go`）对全部写操作留痕：动作 / 结果 / 返回消息（不含用户名与 IP）
- 单实例：`main.go` 装配 Wails `SingleInstance`（UniqueID `infra-ops.desktop`）；重复启动时第二实例自动退出并通知首实例，回调中还原、置前已有窗口
- 大文件：SFTP 与套件资产走 Wails 绑定 + 原生对话框（`desktop/file_service.go`），不走 webview 传输

## 构建与验证

- 发布构建（Windows）：`powershell -ExecutionPolicy Bypass -File script/build-desktop.ps1` → `bin/infra-ops.exe`（GUI 无控制台；图标/manifest 经 syso 嵌入）
- Linux/macOS 本地构建：`bash script/build.sh`（Wails v3 不支持交叉构建）
- 开发调试：`go run . -debug`（控制台日志 + DevTools）；`-cdp` 追加 WebView2 调试端口 9223
- 回归：`go build ./... && go vet ./... && go test ./...`

## Git 提交规范

- 提交信息（commit message）必须使用**中文**，例如：`feat: 主机卡片重构，新增状态色条与延迟徽章`
- 提交作者保持：`Anesthesia <zhanghuajia@hzbxhd.com>`
