#!/bin/bash

# Bakfu (Backup Fusion) - 备份文件合并工具构建脚本

set -e

echo "🚀 构建 Bakfu - 备份文件合并工具"
echo "============================================="

# 检查Go环境
if ! command -v go &> /dev/null; then
    echo "❌ 错误: 需要安装 Go (版本 >= 1.19)"
    echo "请访问 https://golang.org/dl/ 下载安装"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
echo "✅ Go 版本: $GO_VERSION"

# 进入脚本目录
cd "$(dirname "$0")"

# 清理旧的构建文件
echo ""
echo "🧹 清理旧的构建文件..."
rm -rf build/
mkdir -p build

# 构建信息
APP_NAME="bakfu"
VERSION="1.0.0"
BUILD_TIME=$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')
COMMIT_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# 构建标志
BUILD_FLAGS="-ldflags=-s -w -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME -X main.CommitHash=$COMMIT_HASH"

echo ""
echo "📦 开始构建..."

# 构建配置
declare -A PLATFORMS=(
    ["linux/amd64"]="Linux (64位)"
    ["linux/arm64"]="Linux (ARM64)"
    ["darwin/amd64"]="macOS (Intel)"
    ["darwin/arm64"]="macOS (Apple Silicon)"
    ["windows/amd64"]="Windows (64位)"
    ["windows/arm64"]="Windows (ARM64)"
)

# 构建所有平台
for platform in "${!PLATFORMS[@]}"; do
    IFS='/' read -r GOOS GOARCH <<< "$platform"
    output_name="$APP_NAME-$GOOS-$GOARCH"

    if [ "$GOOS" = "windows" ]; then
        output_name="$output_name.exe"
    fi

    echo "  🔧 构建 ${PLATFORMS[$platform]} ($GOOS/$GOARCH)..."

    env GOOS="$GOOS" GOARCH="$GOARCH" go build $BUILD_FLAGS -o "build/$output_name" merge-backups.go

    if [ $? -eq 0 ]; then
        file_size=$(ls -lh "build/$output_name" | awk '{print $5}')
        echo "     ✅ 成功 - 文件大小: $file_size"
    else
        echo "     ❌ 失败"
        exit 1
    fi
done

# 构建当前平台的版本 (无后缀)
echo ""
echo "🎯 构建当前平台版本..."
go build $BUILD_FLAGS -o "build/$APP_NAME" merge-backups.go

if [ $? -eq 0 ]; then
    echo "✅ 当前平台构建成功"
    chmod +x "build/$APP_NAME"
else
    echo "❌ 当前平台构建失败"
    exit 1
fi

echo ""
echo "📊 构建结果:"
echo "============"
ls -lh build/ | grep -v "^total" | while read -r line; do
    echo "  $line"
done

echo ""
echo "🎉 构建完成！"
echo ""
echo "📁 构建文件位置: build/"
echo ""
echo "🚀 快速测试:"
echo "  ./build/$APP_NAME -h"
echo ""
echo "💡 使用示例:"
echo "  ./build/$APP_NAME backup1.zip backup2.zip -o merged.zip"
echo ""
echo "📦 发布准备:"
echo "  - 所有平台的可执行文件已生成"
echo "  - 文件已优化压缩 (-s -w)"
echo "  - 可直接分发，无需任何依赖"
echo ""

# 创建发布包
echo "📦 创建发布包..."
cd build
for file in bakfu-*; do
    if [[ $file == *.exe ]]; then
        # Windows文件
        platform=$(echo "$file" | sed 's/bakfu-//' | sed 's/.exe$//')
        zip -q "${file%.exe}.zip" "$file"
        echo "  📦 ${file%.exe}.zip"
    else
        # Unix文件
        platform=$(echo "$file" | sed 's/bakfu-//')
        tar -czf "$file.tar.gz" "$file"
        echo "  📦 $file.tar.gz"
    fi
done

cd ..
echo ""
echo "✨ 发布包创建完成！"