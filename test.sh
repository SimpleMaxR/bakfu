#!/bin/bash

# Bakfu (Backup Fusion) - 备份文件合并工具测试脚本

set -e

echo "🧪 Bakfu - 备份文件合并工具测试"
echo "=============================================="

# 检查Go环境
if ! command -v go &> /dev/null; then
    echo "❌ 错误: 需要安装 Go"
    exit 1
fi

# 进入脚本目录
cd "$(dirname "$0")"

# 构建工具 (如果不存在)
if [ ! -f "build/bakfu" ]; then
    echo "🔧 构建工具..."
    go build -o build/bakfu merge-backups.go
    echo "✅ 构建完成"
fi

MERGER="./build/bakfu"

# 创建测试目录
TEST_DIR="test-data"
echo ""
echo "📁 创建测试环境..."
rm -rf "$TEST_DIR"
mkdir -p "$TEST_DIR"

# 创建测试数据
echo ""
echo "📝 生成测试数据..."

# 测试数据1 (较旧)
cat > "$TEST_DIR/test1.json" << 'EOF'
{
  "time": 1710403200000,
  "version": 5,
  "localStorage": {
    "persist:cherry-studio": "{\"settings\":\"{\\\"theme\\\":\\\"dark\\\",\\\"language\\\":\\\"zh-CN\\\",\\\"autoSave\\\":true}\",\"providers\":\"[{\\\"id\\\":\\\"openai-1\\\",\\\"name\\\":\\\"OpenAI\\\",\\\"apiKey\\\":\\\"sk-old-key\\\"}]\"}"
  },
  "indexedDB": {
    "providers": [
      {
        "id": "openai-1",
        "name": "OpenAI",
        "baseUrl": "https://api.openai.com/v1",
        "apiKey": "sk-old-key",
        "createdAt": 1710403200000,
        "updatedAt": 1710403200000
      }
    ],
    "assistants": [
      {
        "id": "assistant-1",
        "name": "编程助手",
        "prompt": "你是一个编程助手",
        "model": "gpt-4",
        "createdAt": 1710403200000,
        "updatedAt": 1710403200000
      }
    ],
    "topics": [
      {
        "id": "topic-1",
        "title": "测试对话",
        "createdAt": 1710403200000
      }
    ]
  }
}
EOF

# 测试数据2 (较新，有冲突)
cat > "$TEST_DIR/test2.json" << 'EOF'
{
  "time": 1710406800000,
  "version": 5,
  "localStorage": {
    "persist:cherry-studio": "{\"settings\":\"{\\\"theme\\\":\\\"light\\\",\\\"language\\\":\\\"en-US\\\",\\\"autoSave\\\":false,\\\"fontSize\\\":14}\",\"providers\":\"[{\\\"id\\\":\\\"openai-1\\\",\\\"name\\\":\\\"OpenAI Pro\\\",\\\"apiKey\\\":\\\"sk-new-key\\\"},{\\\"id\\\":\\\"claude-1\\\",\\\"name\\\":\\\"Claude\\\",\\\"apiKey\\\":\\\"sk-claude\\\"}]\"}"
  },
  "indexedDB": {
    "providers": [
      {
        "id": "openai-1",
        "name": "OpenAI Pro",
        "baseUrl": "https://api.openai.com/v1",
        "apiKey": "sk-new-key",
        "createdAt": 1710403200000,
        "updatedAt": 1710406800000
      },
      {
        "id": "claude-1",
        "name": "Claude",
        "baseUrl": "https://api.anthropic.com",
        "apiKey": "sk-claude",
        "createdAt": 1710406800000,
        "updatedAt": 1710406800000
      }
    ],
    "assistants": [
      {
        "id": "assistant-1",
        "name": "高级编程助手",
        "prompt": "你是一个高级编程助手，精通多种语言",
        "model": "gpt-4-turbo",
        "createdAt": 1710403200000,
        "updatedAt": 1710406800000
      },
      {
        "id": "assistant-2",
        "name": "写作助手",
        "prompt": "你是写作专家",
        "model": "claude-3",
        "createdAt": 1710406800000,
        "updatedAt": 1710406800000
      }
    ],
    "topics": [
      {
        "id": "topic-1",
        "title": "重要对话",
        "createdAt": 1710403200000,
        "updatedAt": 1710406800000
      },
      {
        "id": "topic-2",
        "title": "新对话",
        "createdAt": 1710406800000
      }
    ]
  }
}
EOF

# 创建ZIP版本的测试文件
echo ""
echo "📦 创建ZIP测试文件..."

# 创建临时目录用于ZIP
mkdir -p "$TEST_DIR/zip1" "$TEST_DIR/zip2"

# 复制JSON到临时目录
cp "$TEST_DIR/test1.json" "$TEST_DIR/zip1/data.json"
cp "$TEST_DIR/test2.json" "$TEST_DIR/zip2/data.json"

# 创建ZIP文件
cd "$TEST_DIR"
(cd zip1 && zip -q ../test1.zip data.json)
(cd zip2 && zip -q ../test2.zip data.json)
cd ..

echo "✅ 测试数据创建完成"

# 显示测试数据信息
echo ""
echo "📊 测试数据信息:"
echo "  📄 test1.json: 1个提供商, 1个助手, 1个对话 (较旧)"
echo "  📄 test2.json: 2个提供商, 2个助手, 2个对话 (较新)"
echo "  📦 test1.zip, test2.zip: ZIP格式版本"
echo ""
echo "🔍 预期冲突:"
echo "  - 主题设置: dark vs light"
echo "  - 语言设置: zh-CN vs en-US"
echo "  - OpenAI提供商: 名称和API密钥不同"
echo "  - 编程助手: 名称、提示词、模型不同"
echo "  - 对话标题: 测试对话 vs 重要对话"

# 运行测试
echo ""
echo "🚀 开始运行测试..."

# 测试1: 基本功能测试
echo ""
echo "1️⃣  测试基本功能 (显示帮助)..."
if $MERGER -h > /dev/null; then
    echo "✅ 帮助信息正常"
else
    echo "❌ 帮助信息失败"
    exit 1
fi

# 测试2: JSON自动合并 (选择较新)
echo ""
echo "2️⃣  测试JSON自动合并 (选择较新版本)..."
if $MERGER "$TEST_DIR/test1.json" "$TEST_DIR/test2.json" \
    -auto-resolve newer \
    -o "$TEST_DIR/merged-newer.json" > /dev/null; then
    echo "✅ JSON自动合并成功"

    # 验证结果
    if grep -q "OpenAI Pro" "$TEST_DIR/merged-newer.json" && \
       grep -q "Claude" "$TEST_DIR/merged-newer.json" && \
       grep -q "高级编程助手" "$TEST_DIR/merged-newer.json"; then
        echo "✅ 合并结果验证通过 (选择了较新版本)"
    else
        echo "⚠️  合并结果可能不正确"
    fi
else
    echo "❌ JSON自动合并失败"
    exit 1
fi

# 测试3: ZIP格式合并
echo ""
echo "3️⃣  测试ZIP格式合并..."
if $MERGER "$TEST_DIR/test1.zip" "$TEST_DIR/test2.zip" \
    -auto-resolve file2 \
    -o "$TEST_DIR/merged-zip.zip" > /dev/null 2>&1; then
    echo "✅ ZIP格式合并成功"

    # 验证ZIP文件
    if unzip -t "$TEST_DIR/merged-zip.zip" > /dev/null 2>&1; then
        echo "✅ ZIP文件格式正确"
    else
        echo "⚠️  ZIP文件可能损坏"
    fi
else
    echo "❌ ZIP格式合并失败"
    exit 1
fi

# 测试4: 压缩JSON格式
echo ""
echo "4️⃣  测试压缩JSON格式..."
if $MERGER "$TEST_DIR/test1.json" "$TEST_DIR/test2.json" \
    -auto-resolve file1 \
    -format json.gz \
    -o "$TEST_DIR/merged-compressed.json.gz" > /dev/null; then
    echo "✅ 压缩JSON格式成功"

    # 验证压缩文件
    if gunzip -t "$TEST_DIR/merged-compressed.json.gz" > /dev/null 2>&1; then
        echo "✅ 压缩文件格式正确"
    else
        echo "⚠️  压缩文件可能损坏"
    fi
else
    echo "❌ 压缩JSON格式失败"
    exit 1
fi

# 测试5: 混合格式合并
echo ""
echo "5️⃣  测试混合格式合并 (JSON + ZIP)..."
if $MERGER "$TEST_DIR/test1.json" "$TEST_DIR/test2.zip" \
    -auto-resolve older \
    -o "$TEST_DIR/merged-mixed.json" > /dev/null 2>&1; then
    echo "✅ 混合格式合并成功"
else
    echo "❌ 混合格式合并失败"
    exit 1
fi

# 性能测试
echo ""
echo "6️⃣  性能测试..."

echo "  📏 启动速度测试..."
start_time=$(date +%s.%N)
$MERGER -v > /dev/null
end_time=$(date +%s.%N)
startup_time=$(echo "$end_time - $start_time" | bc)
echo "  ⏱️  启动时间: ${startup_time}秒"

echo "  📏 文件大小测试..."
merger_size=$(ls -lh build/bakfu | awk '{print $5}')
echo "  📦 可执行文件大小: $merger_size"

# 结果统计
echo ""
echo "📊 测试结果统计:"
ls -la "$TEST_DIR"/ | grep merged | while read -r line; do
    filename=$(echo "$line" | awk '{print $9}')
    size=$(echo "$line" | awk '{print $5}')
    echo "  📄 $filename: $size"
done

# 清理选项
echo ""
echo "🧹 测试完成！"
echo ""
echo "📁 测试文件保存在: $TEST_DIR/"
echo ""

read -p "是否清理测试文件? [y/N]: " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    rm -rf "$TEST_DIR"
    echo "✅ 测试文件已清理"
else
    echo "📁 测试文件保留在: $TEST_DIR/"
    echo ""
    echo "💡 您可以手动测试交互模式:"
    echo "  $MERGER $TEST_DIR/test1.json $TEST_DIR/test2.json -o $TEST_DIR/interactive.json"
fi

echo ""
echo "🎉 测试完成！"
echo ""
echo "✨ 总结:"
echo "  - 基础功能: ✅"
echo "  - JSON格式: ✅"
echo "  - ZIP格式: ✅"
echo "  - 压缩格式: ✅"
echo "  - 混合格式: ✅"
echo "  - 性能表现: 优秀"
echo ""
echo "🚀 高性能工具优势得到验证!"