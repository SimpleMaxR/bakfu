# Bakfu (Backup Fusion)

Bakfu 是 **Cherry Studio 备份合并工具**。

- legacy（v5，`data.json`）
- direct（v6+，`metadata.json` + `Local Storage` + `IndexedDB`）

## 项目定位

- 目标：把两个 Cherry Studio 备份合并成一个可用备份。
- 范围：只处理 Cherry Studio 相关结构与关键字段（尤其是 `persist:cherry-studio`）。
- 非目标：不承诺兼容任意应用、任意 ZIP 目录结构、任意 JSON 语义。

## 支持的输入与输出

### 输入

- `.zip`（自动识别 legacy/direct）
- `.json`（legacy）
- `.json.gz`（legacy）

### 输出

- `zip`
- `json`（仅双 legacy 输入）
- `json.gz`（仅双 legacy 输入）

> 只要任一输入是 v6+ direct，输出必须是 `zip`。

## 合并行为矩阵

| 输入组合 | 处理方式 | 输出限制 |
|---|---|---|
| legacy + legacy | 细粒度语义合并（`localStorage` + `indexedDB`） | `zip/json/json.gz` |
| direct + direct | `persist:cherry-studio` 语义合并；`IndexedDB/Data` 按目录优先策略 | 仅 `zip` |
| legacy + direct | 按 direct 结构输出；`persist:cherry-studio` 合并；legacy 的 `indexedDB` 作为 `legacy-data.json` 保留 | 仅 `zip` |

## 快速开始

### 本地构建

```bash
go version   # 需要 Go >= 1.19
chmod +x build.sh
./build.sh
```

构建后可执行文件位于 `build/`。

### 运行

```bash
./build/bakfu backup1.zip backup2.zip -o merged.zip
```

## 命令行参数

```bash
bakfu [选项] <文件1> <文件2>

选项:
  -o <文件>                 输出文件路径 (默认: merged-backup.zip)
  -auto-resolve <策略>      自动解决策略: newer|older|file1|file2
  -format <格式>            输出格式: json|json.gz|zip
  -h                        显示帮助
  -v                        显示版本
```

## 常见用法（Cherry Studio）

### 1) 两份 legacy 备份合并

```bash
./bakfu old-a.zip old-b.zip -format json -o merged.json
./bakfu old-a.zip old-b.zip -format json.gz -o merged.json.gz
./bakfu old-a.zip old-b.zip -o merged.zip
```

### 2) 两份 v6+ direct 备份合并

```bash
./bakfu new-a.zip new-b.zip -auto-resolve newer -o merged.zip
```

### 3) legacy + v6+ direct 混合合并

```bash
./bakfu old-v5.zip new-v6.zip -auto-resolve file2 -o merged.zip
```

## 架构与实现细节

本工具核心实现在 `merge-backups.go`，主流程如下：

1. 解析参数与输入校验（`main`）
2. 输入加载与格式识别（`ExtractFromZip` / `LoadFromJSON`）
3. 根据输入类型走 legacy 或 direct 合并分支
4. 执行冲突检测与解决（自动或交互）
5. 输出为目标格式并打印摘要

### 1) 输入识别层

- `ExtractFromZip` 会扫描 ZIP 内容：
  - 命中 `data.json` => legacy
  - 命中 `metadata.json` 且存在 `Local Storage`、`IndexedDB` => direct
- `safeExtractPath` 做 ZIP 路径越界检查，避免解压穿越。

### 2) legacy 合并引擎（v5）

- 入口：`MergeData`
- `localStorage`：重点处理 `persist:cherry-studio`，先反序列化为对象再递归合并。
- `indexedDB`：按表处理，数组记录优先按 `id` 合并。
- 若同 `id` 记录内容不同：保留两份，第二份重写 `id`，并给 `name/title` 添加 `(文件2)` 后缀，避免覆盖。

### 3) direct 合并引擎（v6+）

- 入口：`MergeDirectPractical`
- `persist:cherry-studio`：
  - 从 `Local Storage/leveldb` 读取并标准化
  - 进行对象级语义合并
  - 回写到输出包的 leveldb
- `metadata.json`：以 direct 基础输入为模板，更新时间戳并保证版本不低于 6。
- `IndexedDB`：使用优先输入目录作为主来源（目录级策略，不做记录级解码合并）。
- `Data`：按优先顺序覆盖复制。

### 4) 混合合并（legacy + direct）

- 输出采用 direct 结构。
- legacy 中的 `persist:cherry-studio` 会参与合并。
- legacy 中的 `indexedDB` 由于格式差异无法自动并入 Chromium 二进制库，工具会把原始 `data.json` 以 `legacy-data.json` 带入输出包，便于后续人工恢复。

### 5) 冲突解决机制

- 入口：`resolveConflict`
- 自动策略：
  - `newer` / `older`：基于时间字段选择
  - `file1` / `file2`：固定来源优先
- 交互策略：终端选择 `1/2/d/s`

### 6) 输出层

- 双 legacy：`SaveBackup` 可输出 `zip/json/json.gz`
- 含 direct：`SaveDirectBackup` 仅输出 `zip`

## 关键实现位置（便于二次开发）

- 数据结构定义：`merge-backups.go` 开头（`BackupData`、`DirectMetadata`、`BackupInput`）
- ZIP 识别与提取：`ExtractFromZip`
- legacy 合并：`MergeData`、`mergeLocalStorage`、`mergeIndexedDB`、`mergeMaps`、`mergeArrays`
- direct 合并：`MergeDirectPractical`、`SaveDirectBackup`
- Local Storage leveldb 读写：`readPersistCherryStudio`、`writePersistCherryStudio`
- 冲突处理：`resolveConflict`、`autoResolveConflict`、`interactiveResolve`
- 程序入口：`main`

## 限制与注意事项

1. 本工具只面向 Cherry Studio 备份。
2. 含 v6+ 输入时，仅支持 ZIP 输出。
3. direct 的 `IndexedDB` 不做内容级解码合并，采用目录级优先策略。
4. 混合场景下 legacy 的 `indexedDB` 会以 `legacy-data.json` 保留，不会自动注入 direct 的二进制库。

## 故障排除

### 输出格式报错

如果看到“包含新版备份时仅支持 ZIP 输出”：

```bash
./bakfu a.zip b.zip -format zip -o merged.zip
```

### ZIP 无法识别

请确认输入至少满足其一：

- legacy：包含 `data.json`
- direct：包含 `metadata.json`、`Local Storage/`、`IndexedDB/`

### 权限问题（macOS/Linux）

```bash
chmod +x ./build/bakfu
```

## 构建与发布

### 一键构建

```bash
make build
```

### 运行测试

```bash
make test
```

### 跨平台产物

`build.sh` 会生成 Linux / macOS / Windows 的多架构可执行文件。

## License

MIT
