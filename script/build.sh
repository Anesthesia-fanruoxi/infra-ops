#!/bin/bash
# infra-ops 桌面构建脚本（Linux / macOS 本地执行）
# 用法: bash script/build.sh
#
# 说明：Wails v3 桌面应用不支持跨平台交叉构建，请在目标平台上运行本脚本。
# Windows 桌面构建请使用: powershell -ExecutionPolicy Bypass -File script/build-desktop.ps1
#
# 开发调试无需本脚本：直接 `go run . -debug`（保留控制台日志，-debug 打开 DevTools）。

set -e
cd "$(dirname "$0")/.."

APP_NAME="infra-ops"
BIN_DIR="bin"

OS_NAME=$(uname -s)
echo "=== Building $APP_NAME (Wails v3 desktop, $OS_NAME) ==="

# 图标：build/appicon.png 为入库的源图标；按平台生成缺失的图标产物
if [ "$OS_NAME" = "Darwin" ] && [ ! -f build/darwin/icon.icns ]; then
  echo "[1/2] generating icons from build/appicon.png ..."
  wails3 generate icons -input build/appicon.png || echo "  (icon generation skipped)"
else
  echo "[1/2] icons ok"
fi

# 编译（production 构建标签；macOS/Linux 为窗口子系统，无需 windowsgui）
echo "[2/2] go build ..."
mkdir -p "$BIN_DIR"
go build -tags production -trimpath -buildvcs=false -ldflags="-w -s" -o "$BIN_DIR/$APP_NAME" .

echo "done -> $BIN_DIR/$APP_NAME"
ls -lh "$BIN_DIR/$APP_NAME"
