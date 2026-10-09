# infra-ops

基建运维平台：管理服务器接入、巡检、初始化、中间件安装。桌面应用形态（Wails v3）：单个 exe、不监听任何端口。

## 特性

- **桌面应用**：Wails v3 原生窗口（Windows 为 WebView2 渲染），进程内直连后端引擎，零监听端口
- **agentless**：SSH 直连，无需在目标机部署任何组件
- **单文件分发**：Go 编译 + 前端 embed，一个 exe 即完整系统
- **SQLite**：零依赖数据库，WAL 模式，备份 = 拷贝文件；全部运行配置持久化于 settings 表
- **零配置启动**：首次启动自动生成主密钥，无需手写配置文件
- **凭据安全**：AES-256-GCM 加密落库；无登录、无监听端口，数据仅存本机
- **单实例运行**：重复启动自动拦截，并激活已打开的窗口
- **操作审计**：全量写操作自动留痕——时间、动作、结果与返回消息（不含用户名与 IP）

## 快速开始

### 1. 构建（Windows）

```powershell
powershell -ExecutionPolicy Bypass -File script/build-desktop.ps1
```

产出 `bin/infra-ops.exe`：GUI 子系统（无控制台窗口），应用图标与版本信息已嵌入。

Linux / macOS 在目标平台本地构建（Wails v3 桌面应用不支持交叉构建）：

```bash
bash script/build.sh
```

### 2. 运行

```powershell
bin\infra-ops.exe
```

Windows 需要 WebView2 运行时（Win10/11 系统自带；老系统可从微软官网安装 Evergreen 版）。

开发调试：`go run . -debug`（保留控制台日志；`-debug` 额外打开 DevTools）。自动化测试可追加 `-cdp`（开启本机 WebView2 调试端口 9223）。

### 3. 使用

桌面单机形态下**无需登录**：打开 exe 直接进入主界面（与 Xshell 等本机工具一致），无账号密码、无访问端口，能访问的只有本机用户。

**单实例运行**：启动时检查是否已有实例在运行；重复启动时第二个进程自动退出，并将已打开的窗口还原、置前。

全部写操作（新增/修改/删除/执行）自动写入审计：时间、动作、结果（成功/失败）、返回消息与业务码（不含用户名与 IP），可在「操作日志」页筛选回溯。

### 4. 数据目录

全部数据位于用户数据目录（Windows: `%APPDATA%\infra-ops`）：

```
%APPDATA%\infra-ops\
  data\infra-ops.db     # SQLite（WAL 模式）
  data\assets\...       # 套件离线资产（按 key/version 分层）
```

从旧版（服务端模式，仓库内 `data/`）迁移：关闭应用后，将旧 `data/` 目录整体复制到 `%APPDATA%\infra-ops\` 下即可。不做自动迁移，避免误覆盖现有数据。

## 目录结构

```
main.go          # 入口（数据目录切换、Wails 应用装配、服务注册）
desktop/         # 桌面桥（SSE 事件桥、文件对话框与直传/直落盘服务）
api/             # HTTP 处理 + 业务逻辑（Gin 引擎，进程内挂为资产服务器）
common/          # 公共基建（crypto/sshx/resp/middleware/probe）
model/           # 纯数据结构
store/           # SQLite 存取
config/          # 配置加载
router/          # 路由装配
template/        # 前端（go:embed）+ wails-shim.js（EventSource 事件桥覆盖）
build/           # 桌面构建资产（应用图标、Windows 资源清单）
script/          # 构建脚本（build-desktop.ps1 / build.sh）
```

## 技术栈

- Wails v3（桌面外壳与资产服务器；Beta 阶段，go.mod 锁定版本）
- Go 1.27.1 + Gin（引擎直接作为 Wails 资产服务器 handler）
- SQLite（modernc.org/sqlite，纯 Go 无 cgo）
- Vue3 + Element Plus（全局构建版，无 npm）
- AES-256-GCM 凭据加密

## 桌面端三个关键改造

1. **SSE 流式**：WebView2 下资产服务器直转 SSE 会阻塞，页面通过 `wails-shim.js` 用 Wails 事件桥替换 `window.EventSource`（前端调用点零修改）；断流 3 秒自动重连，语义与浏览器一致。
2. **单实例**：内置 Wails SingleInstance（命名互斥体 + 隐藏消息窗口）；重复启动时第二个实例检测失败后立即退出，并向首实例发送通知，首实例还原并置前窗口。
3. **大文件传输**：webview 内的 multipart/blob 会经资产服务器内存缓冲，SFTP 上传/下载与套件资产上传改为 Wails 绑定 + 原生文件对话框，Go 端流式直传/直落盘。

## 已知局限

- exe 未签名：首次运行可能触发 Windows SmartScreen 提示（选择"仍要运行"）
- macOS/Linux 桌面构建需在各自平台执行，本仓库优先保障 Windows
- 无系统托盘；关闭窗口即退出应用

## API

以下接口均在进程内同源调用（供桌面窗口使用），并非外部 HTTP 服务：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | /api/hosts/batch | 批量新增主机 |
| POST | /api/hosts/{id}/test | 连接测试 |
| GET | /api/sse/hosts | 主机状态实时推送（桌面经 SSE 桥） |
| GET | /api/healthz | 存活探针 |
