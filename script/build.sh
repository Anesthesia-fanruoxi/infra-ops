#!/bin/bash
# infra-ops 交叉编译脚本
# 用法: bash script/build.sh

set -e

VERSION="0.1.0"
BUILD_TIME=$(date '+%Y-%m-%d %H:%M:%S')
MODULE="github.com/Anesthesia-fanruoxi/infra-ops"

LDFLAGS="-s -w"

echo "=== Building infra-ops ==="
echo "Version: $VERSION"
echo "Build Time: $BUILD_TIME"

# Windows (开发环境)
echo "[1/2] Building for Windows amd64..."
GOOS=windows GOARCH=amd64 go build -ldflags="$LDFLAGS" -o dist/infra-ops.exe .
echo "  -> dist/infra-ops.exe"

# Linux (生产环境)
echo "[2/2] Building for Linux amd64..."
GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -o dist/infra-ops .
echo "  -> dist/infra-ops"

echo ""
echo "=== Build complete ==="
ls -lh dist/
