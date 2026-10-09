# infra-ops 桌面构建脚本（Windows）
# 用法: powershell -ExecutionPolicy Bypass -File script/build-desktop.ps1
#
# 产出 bin/infra-ops.exe：
#   - GUI 子系统（-H windowsgui，无控制台窗口）
#   - 嵌入应用图标（build/windows/icon.ico）与 DPI 感知清单（wails.exe.manifest）
#
# 开发调试无需本脚本：直接 `go run . -debug`（保留控制台日志，-debug 打开 DevTools）。
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$AppName = "infra-ops"
$BinDir = "bin"
$Syso = "wails_windows_amd64.syso"

Write-Host "=== Building $AppName (Wails v3 desktop, windows/amd64) ==="

# 1) 图标：build/appicon.png 为入库的源图标；icon.ico 缺失时才重新生成
if (-not (Test-Path "build/windows/icon.ico")) {
    if (Test-Path "build/appicon.png") {
        Write-Host "[1/4] generating icons from build/appicon.png ..."
        wails3 generate icons -input build/appicon.png
    } else {
        Write-Warning "build/appicon.png 缺失，跳过图标生成（exe 将使用默认图标）"
    }
} else {
    Write-Host "[1/4] build/windows/icon.ico present, skip icon generation"
}

# 2) 生成 windows syso：把图标、manifest、版本信息编入 exe 资源
Write-Host "[2/4] generating windows resource (syso) ..."
Push-Location build
try {
    wails3 generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out "../$Syso"
} finally {
    Pop-Location
}

# 3) 编译（GUI 子系统）
#    标签说明（踩过坑，别删 devtools）：
#      Wails 用构建 tag 决定 DevTools 是否可用 ——
#      webview_window_windows_devtools.go      tag: windows && (!production || devtools)  → OpenDevTools 有实现
#      webview_window_windows_production.go   tag: windows && production && !devtools    → OpenDevTools 是空函数
#      即：只打 production 时 window.OpenDevTools() 静默无效，-debug 参数形同虚设。
#      同时打 production + devtools 才落到 devtools 那份实现（两者互补，不冲突）。
#    另外 F12 不能直接用：Wails 硬编码 settings.PutAreBrowserAcceleratorKeysEnabled(false)，
#    且 Windows 侧不投递 windowKeyEvents（只有 darwin/linux 有），所以 RegisterKeyBinding("f12") 无效。
#    F12 由前端 keydown 监听 + Go 侧暴露的 OpenDevTools 服务实现，见 desktop/devtools.go。
Write-Host "[3/4] go build ..."
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
try {
    go build -tags "production devtools" -trimpath -buildvcs=false -ldflags="-w -s -H windowsgui" -o "$BinDir/$AppName.exe" .
} finally {
    Remove-Item $Syso -ErrorAction SilentlyContinue
}

# 4) 结果
$exe = Get-Item "$BinDir/$AppName.exe"
$sizeMB = [math]::Round($exe.Length / 1MB, 1)
Write-Host "[4/4] done -> $($exe.FullName) ($sizeMB MB)"
