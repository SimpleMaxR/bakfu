# Bakfu (Backup Fusion) - 备份文件合并工具 Makefile

.PHONY: build test clean install help run dev release check

# 默认目标
all: build

# 应用信息
APP_NAME := bakfu
VERSION := 1.0.0
BUILD_DIR := build

# Go构建标志
LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION) -X main.BuildTime=$(shell date -u '+%Y-%m-%d_%H:%M:%S_UTC')"

# 构建当前平台版本
build:
	@echo "🔧 构建Go版本..."
	@mkdir -p $(BUILD_DIR)
	@go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) merge-backups.go
	@echo "✅ 构建完成: $(BUILD_DIR)/$(APP_NAME)"

# 构建所有平台版本
build-all:
	@echo "📦 构建所有平台版本..."
	@chmod +x build.sh
	@./build.sh

# 快速构建 (开发模式)
dev:
	@echo "⚡ 快速构建 (开发模式)..."
	@go build -o $(BUILD_DIR)/$(APP_NAME)-dev merge-backups.go
	@echo "✅ 开发版本: $(BUILD_DIR)/$(APP_NAME)-dev"

# 运行测试
test: build
	@echo "🧪 运行测试..."
	@chmod +x test.sh
	@./test.sh

# 安装到系统路径
install: build
	@echo "📥 安装到系统..."
	@sudo cp $(BUILD_DIR)/$(APP_NAME) /usr/local/bin/
	@echo "✅ 已安装到 /usr/local/bin/$(APP_NAME)"

# 卸载
uninstall:
	@echo "🗑️  卸载..."
	@sudo rm -f /usr/local/bin/$(APP_NAME)
	@echo "✅ 已卸载"

# 清理构建文件
clean:
	@echo "🧹 清理构建文件..."
	@rm -rf $(BUILD_DIR)
	@rm -rf test-data
	@rm -rf temp-merge
	@rm -f *.zip *.json *.json.gz
	@echo "✅ 清理完成"

# 运行示例
run: build
	@echo "🎯 运行示例..."
	@echo "显示帮助信息:"
	@./$(BUILD_DIR)/$(APP_NAME) -h

# 快速合并示例
demo: test
	@echo ""
	@echo "🎬 运行演示..."
	@echo "基础合并示例:"
	@./$(BUILD_DIR)/$(APP_NAME) test-data/test1.json test-data/test2.json \
		-auto-resolve newer \
		-o demo-merged.json
	@echo "✅ 演示完成: demo-merged.json"

# 性能基准测试
benchmark: build
	@echo "📊 性能基准测试..."
	@echo ""
	@echo "启动速度测试:"
	@time ./$(BUILD_DIR)/$(APP_NAME) -v
	@echo ""
	@echo "内存使用测试 (需要安装 time 命令):"
	@if command -v /usr/bin/time >/dev/null 2>&1; then \
		echo "运行内存测试..."; \
		/usr/bin/time -l ./$(BUILD_DIR)/$(APP_NAME) -h 2>&1 | grep "maximum resident set size"; \
	else \
		echo "跳过内存测试 (需要 GNU time)"; \
	fi
	@echo ""
	@echo "文件大小:"
	@ls -lh $(BUILD_DIR)/$(APP_NAME)

# 代码检查
check:
	@echo "🔍 代码检查..."
	@echo "语法检查:"
	@go vet merge-backups.go
	@echo "✅ 语法检查通过"
	@echo ""
	@echo "格式检查:"
	@gofmt -d merge-backups.go
	@echo "✅ 格式检查通过"
	@echo ""
	@echo "依赖检查:"
	@go mod verify
	@echo "✅ 依赖检查通过"

# 格式化代码
fmt:
	@echo "🎨 格式化代码..."
	@go fmt merge-backups.go
	@echo "✅ 代码格式化完成"

# 创建发布版本
release: clean build-all
	@echo "🚀 准备发布版本..."
	@echo "版本: $(VERSION)"
	@echo "构建时间: $(shell date)"
	@echo ""
	@echo "📦 发布文件:"
	@ls -la $(BUILD_DIR)/ | grep -E "\.(zip|tar\.gz)$$" || echo "  (运行 build-all 生成发布包)"
	@echo ""
	@echo "✅ 发布准备完成!"

# 交互式向导
wizard: build
	@echo "🧙 备份合并向导"
	@echo "================"
	@echo ""
	@echo "请选择操作："
	@echo "  1) 合并两个备份文件"
	@echo "  2) 运行测试"
	@echo "  3) 查看帮助"
	@echo "  4) 性能测试"
	@echo ""
	@read -p "请输入选择 (1-4): " choice; \
	case $$choice in \
		1) make merge-wizard ;; \
		2) make test ;; \
		3) ./$(BUILD_DIR)/$(APP_NAME) -h ;; \
		4) make benchmark ;; \
		*) echo "❌ 无效选择" ;; \
	esac

# 合并向导
merge-wizard: build
	@echo ""
	@echo "📁 文件合并向导"
	@echo "==============="
	@echo ""
	@echo "支持格式: .zip, .json, .json.gz"
	@echo ""
	@read -p "第一个备份文件: " file1; \
	read -p "第二个备份文件: " file2; \
	read -p "输出文件 (默认: merged.zip): " output; \
	output=$${output:-merged.zip}; \
	echo ""; \
	echo "冲突解决策略:"; \
	echo "  1) 交互式选择"; \
	echo "  2) 自动选择较新版本"; \
	echo "  3) 自动选择第一个文件"; \
	echo "  4) 自动选择第二个文件"; \
	read -p "请选择 (1-4): " strategy; \
	case $$strategy in \
		2) strategy_flag="-auto-resolve newer" ;; \
		3) strategy_flag="-auto-resolve file1" ;; \
		4) strategy_flag="-auto-resolve file2" ;; \
		*) strategy_flag="" ;; \
	esac; \
	echo ""; \
	echo "🚀 开始合并..."; \
	./$(BUILD_DIR)/$(APP_NAME) "$$file1" "$$file2" -o "$$output" $$strategy_flag

# 显示帮助
help:
	@echo "Cherry Studio 备份合并工具 - Go版本 Makefile"
	@echo ""
	@echo "可用命令:"
	@echo "  build        构建当前平台版本"
	@echo "  build-all    构建所有平台版本"
	@echo "  dev          快速构建 (开发模式)"
	@echo "  test         运行完整测试套件"
	@echo "  install      安装到系统路径"
	@echo "  uninstall    从系统卸载"
	@echo "  clean        清理构建文件"
	@echo "  run          显示基本用法"
	@echo "  demo         运行演示"
	@echo "  benchmark    性能基准测试"
	@echo "  check        代码质量检查"
	@echo "  fmt          格式化代码"
	@echo "  release      准备发布版本"
	@echo "  wizard       交互式向导"
	@echo "  help         显示此帮助"
	@echo ""
	@echo "快速开始:"
	@echo "  make build                    # 构建"
	@echo "  make test                     # 测试"
	@echo "  make wizard                   # 交互式使用"
	@echo ""
	@echo "直接使用:"
	@echo "  ./build/$(APP_NAME) backup1.zip backup2.zip"

# 开发者工具
dev-tools:
	@echo "🛠️  检查开发工具..."
	@echo "Go版本:"
	@go version
	@echo ""
	@echo "可用工具:"
	@command -v gofmt >/dev/null && echo "  ✅ gofmt" || echo "  ❌ gofmt"
	@command -v go >/dev/null && echo "  ✅ go" || echo "  ❌ go"
	@command -v git >/dev/null && echo "  ✅ git" || echo "  ❌ git"
	@command -v zip >/dev/null && echo "  ✅ zip" || echo "  ❌ zip"
	@command -v unzip >/dev/null && echo "  ✅ unzip" || echo "  ❌ unzip"

# 监视文件变化并自动构建 (需要 entr)
watch:
	@if command -v entr >/dev/null; then \
		echo "👁️  监视文件变化..."; \
		echo "merge-backups.go" | entr -c make dev; \
	else \
		echo "❌ 需要安装 entr: brew install entr"; \
	fi

# Docker构建 (可选)
docker-build:
	@echo "🐳 Docker构建..."
	@docker run --rm -v "$$PWD":/usr/src/app -w /usr/src/app golang:1.19 \
		go build -o build/$(APP_NAME)-linux merge-backups.go
	@echo "✅ Docker构建完成"