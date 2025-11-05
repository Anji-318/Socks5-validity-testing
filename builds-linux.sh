#!/bin/bash
set -e

# 此脚本使用 Unix (LF) 行尾格式以确保兼容性。

# 设置默认 Go 文件为 aigo.go，或使用命令行参数指定的文件
GO_FILE=${1:-aigo.go}
VERSION="v1.0.0"
VERSION=${2:-v1.0.3}  # 从参数获取版本号，默认为 v1.0.0

echo "--- 开始为不同平台编译可执行程序 ---"

# 如果 builds 目录不存在，则创建它
if [ ! -d "builds" ]; then
    mkdir builds
    echo "✅ 创建 'builds' 目录"
fi

# Windows 64-bit
echo "▶️ 编译 Windows (amd64)..."
GOOS=windows GOARCH=amd64 go build -o builds/proxy-checker-${VERSION}-windows-amd64.exe ${GO_FILE}
echo "✅ Windows 编译完成。"

# Linux 64-bit (most desktops/servers)
echo "▶️ 编译 Linux (amd64)..."
GOOS=linux GOARCH=amd64 go build -o builds/proxy-checker-${VERSION}-linux-amd64 ${GO_FILE}
echo "✅ Linux 编译完成。"

# macOS 64-bit (Intel)
echo "▶️ 编译 macOS (Intel)..."
GOOS=darwin GOARCH=amd64 go build -o builds/proxy-checker-${VERSION}-darwin-amd64 ${GO_FILE}
echo "✅ macOS (Intel) 编译完成。"

# macOS ARM64 (Apple Silicon)
echo "▶️ 编译 macOS (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -o builds/proxy-checker-${VERSION}-darwin-arm64 ${GO_FILE}
echo "✅ macOS (Apple Silicon) 编译完成。"

# Linux ARM64 (e.g., Raspberry Pi 4, Termux on modern phones)
echo "▶️ 编译 Linux (ARM64)..."
GOOS=linux GOARCH=arm64 go build -o builds/proxy-checker-${VERSION}-linux-arm64 ${GO_FILE}
echo "✅ Linux (ARM64) 编译完成。"

# Android ARM64 (e.g., Termux on Android devices)
echo "▶️ 编译 Android (arm64) for Termux..."
GOOS=android GOARCH=arm64 go build -o builds/proxy-checker-${VERSION}-android-arm64 ${GO_FILE}
echo "✅ Android (arm64) 编译完成。"

echo "" # 添加空行以提高可读性
echo "--- 所有编译完成！文件已存放在 builds/ 目录下。---"
