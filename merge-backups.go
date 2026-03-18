package main

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// 备份数据结构
type BackupData struct {
	Time        int64                  `json:"time"`
	Version     int                    `json:"version"`
	LocalStorage map[string]interface{} `json:"localStorage"`
	IndexedDB   map[string]interface{} `json:"indexedDB"`
}

// 冲突类型
var ConflictTypes = map[string]string{
	"providers":   "AI服务提供商",
	"models":      "模型配置",
	"assistants":  "助手配置",
	"settings":    "应用设置",
	"knowledge":   "知识库",
	"mcp_servers": "MCP服务器",
	"prompts":     "提示词",
	"localStorage": "localStorage配置",
}

// 冲突信息
type Conflict struct {
	Type     string      `json:"type"`
	Key      string      `json:"key"`
	Value1   interface{} `json:"value1"`
	Value2   interface{} `json:"value2"`
	Context  string      `json:"context"`
	Resolved interface{} `json:"resolved,omitempty"`
}

// 合并器
type BackupMerger struct {
	AutoResolve string
	Conflicts   []Conflict
	Reader      *bufio.Reader
}

// 创建新的合并器
func NewBackupMerger(autoResolve string) *BackupMerger {
	return &BackupMerger{
		AutoResolve: autoResolve,
		Conflicts:   make([]Conflict, 0),
		Reader:      bufio.NewReader(os.Stdin),
	}
}

// 从ZIP文件提取备份数据
func (bm *BackupMerger) ExtractFromZip(zipPath, extractDir string) (*BackupData, error) {
	fmt.Printf("📦 解压备份文件: %s\n", zipPath)

	// 创建解压目录
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return nil, fmt.Errorf("创建解压目录失败: %v", err)
	}

	// 打开ZIP文件
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("打开ZIP文件失败: %v", err)
	}
	defer r.Close()

	var dataFile *zip.File

	// 解压所有文件并查找data.json
	for _, f := range r.File {
		if f.Name == "data.json" {
			dataFile = f
		}

		// 解压文件
		extractPath := filepath.Join(extractDir, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(extractPath, f.FileInfo().Mode())
			continue
		}

		if err := bm.extractFile(f, extractPath); err != nil {
			return nil, fmt.Errorf("解压文件 %s 失败: %v", f.Name, err)
		}
	}

	if dataFile == nil {
		return nil, fmt.Errorf("ZIP文件中未找到 data.json")
	}

	// 读取并解析data.json
	rc, err := dataFile.Open()
	if err != nil {
		return nil, fmt.Errorf("打开data.json失败: %v", err)
	}
	defer rc.Close()

	var data BackupData
	if err := json.NewDecoder(rc).Decode(&data); err != nil {
		return nil, fmt.Errorf("解析data.json失败: %v", err)
	}

	return &data, nil
}

// 解压单个文件
func (bm *BackupMerger) extractFile(f *zip.File, destPath string) error {
	// 创建目标目录
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	// 打开ZIP中的文件
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	// 创建目标文件
	outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.FileInfo().Mode())
	if err != nil {
		return err
	}
	defer outFile.Close()

	// 复制内容
	_, err = io.Copy(outFile, rc)
	return err
}

// 从JSON文件加载备份数据
func (bm *BackupMerger) LoadFromJSON(jsonPath string) (*BackupData, error) {
	fmt.Printf("📄 读取JSON文件: %s\n", jsonPath)

	var reader io.Reader
	file, err := os.Open(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	// 检查是否是gzip压缩文件
	if strings.HasSuffix(strings.ToLower(jsonPath), ".gz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("解压gzip文件失败: %v", err)
		}
		defer gzReader.Close()
		reader = gzReader
	} else {
		reader = file
	}

	var data BackupData
	if err := json.NewDecoder(reader).Decode(&data); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %v", err)
	}

	return &data, nil
}

// 保存备份数据
func (bm *BackupMerger) SaveBackup(data *BackupData, outputPath, format string) error {
	fmt.Printf("💾 保存合并结果: %s\n", outputPath)

	// 创建输出目录
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %v", err)
	}

	switch format {
	case "json":
		return bm.saveAsJSON(data, outputPath, false)
	case "json.gz":
		return bm.saveAsJSON(data, outputPath, true)
	case "zip":
		return bm.saveAsZip(data, outputPath)
	default:
		// 根据文件扩展名自动判断
		ext := strings.ToLower(filepath.Ext(outputPath))
		if ext == ".gz" || strings.HasSuffix(strings.ToLower(outputPath), ".json.gz") {
			return bm.saveAsJSON(data, outputPath, true)
		} else if ext == ".json" {
			return bm.saveAsJSON(data, outputPath, false)
		} else {
			return bm.saveAsZip(data, outputPath)
		}
	}
}

// 保存为JSON格式
func (bm *BackupMerger) saveAsJSON(data *BackupData, outputPath string, compress bool) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var writer io.Writer = file
	if compress {
		gzWriter := gzip.NewWriter(file)
		defer gzWriter.Close()
		writer = gzWriter
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// 保存为ZIP格式
func (bm *BackupMerger) saveAsZip(data *BackupData, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()

	// 创建data.json文件
	jsonWriter, err := zipWriter.Create("data.json")
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(jsonWriter)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// 合并两个备份数据
func (bm *BackupMerger) MergeData(data1, data2 *BackupData) (*BackupData, error) {
	fmt.Println("\n🔄 开始合并数据...")

	merged := &BackupData{
		Time:        maxInt64(data1.Time, data2.Time),
		Version:     maxInt(data1.Version, data2.Version),
		LocalStorage: make(map[string]interface{}),
		IndexedDB:   make(map[string]interface{}),
	}

	// 合并LocalStorage
	if err := bm.mergeLocalStorage(data1.LocalStorage, data2.LocalStorage, merged.LocalStorage); err != nil {
		return nil, err
	}

	// 合并IndexedDB
	if err := bm.mergeIndexedDB(data1.IndexedDB, data2.IndexedDB, merged.IndexedDB); err != nil {
		return nil, err
	}

	return merged, nil
}

// 合并LocalStorage
func (bm *BackupMerger) mergeLocalStorage(ls1, ls2, merged map[string]interface{}) error {
	fmt.Println("\n📱 合并 localStorage 数据...")

	// 处理persist:cherry-studio数据
	persist1 := make(map[string]interface{})
	persist2 := make(map[string]interface{})

	if val1, ok := ls1["persist:cherry-studio"]; ok {
		if str, ok := val1.(string); ok {
			json.Unmarshal([]byte(str), &persist1)
		}
	}

	if val2, ok := ls2["persist:cherry-studio"]; ok {
		if str, ok := val2.(string); ok {
			json.Unmarshal([]byte(str), &persist2)
		}
	}

	mergedPersist, err := bm.mergeMaps(persist1, persist2, "localStorage.persist:cherry-studio")
	if err != nil {
		return err
	}

	// 序列化回字符串
	persistBytes, _ := json.Marshal(mergedPersist)
	merged["persist:cherry-studio"] = string(persistBytes)

	// 合并其他localStorage项
	for key, value := range ls1 {
		if key != "persist:cherry-studio" {
			merged[key] = value
		}
	}

	for key, value := range ls2 {
		if key != "persist:cherry-studio" && merged[key] == nil {
			merged[key] = value
		}
	}

	return nil
}

// 合并IndexedDB
func (bm *BackupMerger) mergeIndexedDB(db1, db2, merged map[string]interface{}) error {
	fmt.Println("\n🗃️  合并 IndexedDB 数据...")

	// 获取所有表名
	tables := make(map[string]bool)
	for table := range db1 {
		tables[table] = true
	}
	for table := range db2 {
		tables[table] = true
	}

	// 合并每个表
	for tableName := range tables {
		fmt.Printf("  处理表: %s\n", tableName)

		var table1, table2 []interface{}

		if val1, ok := db1[tableName]; ok {
			if arr, ok := val1.([]interface{}); ok {
				table1 = arr
			}
		}

		if val2, ok := db2[tableName]; ok {
			if arr, ok := val2.([]interface{}); ok {
				table2 = arr
			}
		}

		mergedTable, err := bm.mergeTable(table1, table2, tableName)
		if err != nil {
			return err
		}

		merged[tableName] = mergedTable
	}

	return nil
}

// 合并表数据（委托给 mergeArrays）
func (bm *BackupMerger) mergeTable(table1, table2 []interface{}, tableName string) ([]interface{}, error) {
	return bm.mergeArrays(table1, table2, tableName)
}

// 合并对象数组（按 ID 合并，递归处理差异）
func (bm *BackupMerger) mergeArrays(arr1, arr2 []interface{}, context string) ([]interface{}, error) {
	merged := make([]interface{}, 0)
	processedIds := make(map[string]bool)

	map1 := make(map[string]interface{})
	map2 := make(map[string]interface{})

	for i, item := range arr1 {
		if itemMap, ok := item.(map[string]interface{}); ok {
			id := getItemId(itemMap, i)
			map1[id] = item
		}
	}
	for i, item := range arr2 {
		if itemMap, ok := item.(map[string]interface{}); ok {
			id := getItemId(itemMap, i)
			map2[id] = item
		}
	}

	for id, item1 := range map1 {
		processedIds[id] = true
		if item2, exists := map2[id]; exists {
			if !reflect.DeepEqual(item1, item2) {
				resolved, err := bm.mergeValue(item1, item2, fmt.Sprintf("%s[%s]", context, id))
				if err != nil {
					return nil, err
				}
				merged = append(merged, resolved)
			} else {
				merged = append(merged, item1)
			}
		} else {
			merged = append(merged, item1)
		}
	}
	for id, item2 := range map2 {
		if !processedIds[id] {
			merged = append(merged, item2)
		}
	}

	return merged, nil
}

// 合并单个值：智能分发数组/对象/标量
func (bm *BackupMerger) mergeValue(value1, value2 interface{}, context string) (interface{}, error) {
	if reflect.DeepEqual(value1, value2) {
		return value1, nil
	}

	arr1, isArr1 := value1.([]interface{})
	arr2, isArr2 := value2.([]interface{})
	if isArr1 && isArr2 {
		if hasIDField(arr1) || hasIDField(arr2) {
			return bm.mergeArrays(arr1, arr2, context)
		}
		// 无 ID 的普通数组：整体冲突
		return bm.resolveConflict(Conflict{
			Type:    getConflictTypeFromContext(context),
			Key:     context,
			Value1:  value1,
			Value2:  value2,
			Context: context,
		})
	}

	map1, isMap1 := value1.(map[string]interface{})
	map2, isMap2 := value2.(map[string]interface{})
	if isMap1 && isMap2 {
		return bm.mergeMaps(map1, map2, context)
	}

	// 标量冲突
	return bm.resolveConflict(Conflict{
		Type:    getConflictTypeFromContext(context),
		Key:     context,
		Value1:  value1,
		Value2:  value2,
		Context: context,
	})
}

// 合并 map 数据（按 key 逐一智能合并）
func (bm *BackupMerger) mergeMaps(map1, map2 map[string]interface{}, context string) (map[string]interface{}, error) {
	merged := make(map[string]interface{})

	for key, value := range map1 {
		merged[key] = value
	}

	for key, value2 := range map2 {
		if key == "_persist" {
			continue
		}

		childCtx := fmt.Sprintf("%s.%s", context, key)
		if value1, exists := merged[key]; exists {
			if !reflect.DeepEqual(value1, value2) {
				resolved, err := bm.mergeValue(value1, value2, childCtx)
				if err != nil {
					return nil, err
				}
				merged[key] = resolved
			}
		} else {
			merged[key] = value2
		}
	}

	return merged, nil
}

// 解决冲突
func (bm *BackupMerger) resolveConflict(conflict Conflict) (interface{}, error) {
	bm.Conflicts = append(bm.Conflicts, conflict)

	// 自动解决策略
	if bm.AutoResolve != "" {
		return bm.autoResolveConflict(conflict), nil
	}

	// 交互式解决
	return bm.interactiveResolve(conflict)
}

// 自动解决冲突
func (bm *BackupMerger) autoResolveConflict(conflict Conflict) interface{} {
	switch bm.AutoResolve {
	case "newer":
		time1 := getTimestamp(conflict.Value1)
		time2 := getTimestamp(conflict.Value2)
		if time2 > time1 {
			return conflict.Value2
		}
		return conflict.Value1
	case "older":
		time1 := getTimestamp(conflict.Value1)
		time2 := getTimestamp(conflict.Value2)
		if time1 < time2 {
			return conflict.Value1
		}
		return conflict.Value2
	case "file1":
		return conflict.Value1
	case "file2":
		return conflict.Value2
	default:
		return conflict.Value1
	}
}

// 交互式解决冲突
func (bm *BackupMerger) interactiveResolve(conflict Conflict) (interface{}, error) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("⚠️  发现冲突: %s\n", conflict.Type)
	fmt.Printf("📍 位置: %s\n", conflict.Context)
	fmt.Println(strings.Repeat("=", 80))

	fmt.Println("\n📄 选项 1 (来自第一个文件):")
	bm.printValue(conflict.Value1)

	fmt.Println("\n📄 选项 2 (来自第二个文件):")
	bm.printValue(conflict.Value2)

	for {
		fmt.Print("\n请选择解决方案 [1/2/d(diff)/s(skip)]: ")
		input, err := bm.Reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		choice := strings.TrimSpace(strings.ToLower(input))
		switch choice {
		case "1":
			return conflict.Value1, nil
		case "2":
			return conflict.Value2, nil
		case "d":
			bm.showDiff(conflict.Value1, conflict.Value2)
			continue
		case "s":
			return conflict.Value1, nil
		default:
			fmt.Println("❌ 无效选择，请输入 1、2、d 或 s")
			continue
		}
	}
}

// 打印值
func (bm *BackupMerger) printValue(value interface{}) {
	if valueMap, ok := value.(map[string]interface{}); ok {
		count := 0
		for key, val := range valueMap {
			if count >= 5 {
				fmt.Println("  ...")
				break
			}
			fmt.Printf("  %s: %v\n", key, formatValue(val))
			count++
		}
	} else {
		fmt.Printf("  %v\n", formatValue(value))
	}
}

// 显示差异
func (bm *BackupMerger) showDiff(value1, value2 interface{}) {
	fmt.Println("\n🔍 详细差异:")

	if map1, ok1 := value1.(map[string]interface{}); ok1 {
		if map2, ok2 := value2.(map[string]interface{}); ok2 {
			keys := make(map[string]bool)
			for key := range map1 {
				keys[key] = true
			}
			for key := range map2 {
				keys[key] = true
			}

			for key := range keys {
				val1 := map1[key]
				val2 := map2[key]

				if !reflect.DeepEqual(val1, val2) {
					fmt.Printf("\n  %s:\n", key)
					fmt.Printf("    文件1: %v\n", formatValue(val1))
					fmt.Printf("    文件2: %v\n", formatValue(val2))
				}
			}
		}
	}
}

// 打印摘要
func (bm *BackupMerger) PrintSummary() {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("📊 合并摘要")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("总冲突数量: %d\n", len(bm.Conflicts))

	if len(bm.Conflicts) > 0 {
		conflictsByType := make(map[string]int)
		for _, conflict := range bm.Conflicts {
			conflictsByType[conflict.Type]++
		}

		fmt.Println("\n冲突分布:")

		// 排序输出
		var types []string
		for t := range conflictsByType {
			types = append(types, t)
		}
		sort.Strings(types)

		for _, t := range types {
			fmt.Printf("  %s: %d\n", t, conflictsByType[t])
		}
	}
}

// 工具函数
func getItemId(item map[string]interface{}, index int) string {
	if id, ok := item["id"].(string); ok {
		return id
	}
	return fmt.Sprintf("__index_%d", index)
}

func getConflictType(tableName string) string {
	if conflictType, ok := ConflictTypes[tableName]; ok {
		return conflictType
	}
	return tableName
}

// 从 context 路径推断冲突类型（如 "localStorage.providers[openai]" → "AI服务提供商"）
func getConflictTypeFromContext(context string) string {
	for key, label := range ConflictTypes {
		if strings.Contains(context, key) {
			return label
		}
	}
	return context
}

// 检查数组中是否存在带 id 字段的对象
func hasIDField(arr []interface{}) bool {
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			if _, hasID := m["id"]; hasID {
				return true
			}
		}
	}
	return false
}

func getTimestamp(value interface{}) int64 {
	if valueMap, ok := value.(map[string]interface{}); ok {
		if updatedAt, ok := valueMap["updatedAt"].(float64); ok {
			return int64(updatedAt)
		}
		if createdAt, ok := valueMap["createdAt"].(float64); ok {
			return int64(createdAt)
		}
	}
	return 0
}

func formatValue(value interface{}) string {
	if value == nil {
		return "null"
	}

	switch v := value.(type) {
	case string:
		if len(v) > 50 {
			return v[:50] + "..."
		}
		return v
	case map[string]interface{}:
		return "[Object]"
	case []interface{}:
		return fmt.Sprintf("[Array:%d]", len(v))
	default:
		str := fmt.Sprintf("%v", v)
		if len(str) > 50 {
			return str[:50] + "..."
		}
		return str
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	var (
		output      = flag.String("o", "merged-backup.zip", "输出文件路径")
		autoResolve = flag.String("auto-resolve", "", "自动解决策略: newer|older|file1|file2")
		format      = flag.String("format", "", "输出格式: json|json.gz|zip (默认根据文件扩展名判断)")
		help        = flag.Bool("h", false, "显示帮助信息")
		version     = flag.Bool("v", false, "显示版本信息")
	)

	flag.Parse()

	if *help {
		fmt.Println(`Bakfu - 备份文件合并工具

用法: bakfu [选项] <文件1> <文件2>

参数:
  文件1                     第一个备份文件路径 (.zip, .json, .json.gz)
  文件2                     第二个备份文件路径

选项:
  -o <文件>                 输出文件路径 (默认: "merged-backup.zip")
  -auto-resolve <策略>      自动解决策略: newer|older|file1|file2
  -format <格式>            输出格式: json|json.gz|zip
  -h                        显示帮助信息
  -v                        显示版本信息

示例:
  merge-backups backup1.zip backup2.zip
  merge-backups backup1.json backup2.json -o merged.json
  merge-backups backup1.zip backup2.json.gz -auto-resolve newer
  merge-backups backup1.json backup2.json -format json.gz -o merged.json.gz`)
		return
	}

	if *version {
		fmt.Println("Bakfu v1.0.0")
		return
	}

	args := flag.Args()
	if len(args) != 2 {
		fmt.Println("❌ 请提供两个备份文件路径")
		fmt.Println("使用 -h 查看帮助信息")
		os.Exit(1)
	}

	file1, file2 := args[0], args[1]

	// 检查输入文件
	if _, err := os.Stat(file1); os.IsNotExist(err) {
		fmt.Printf("❌ 文件不存在: %s\n", file1)
		os.Exit(1)
	}

	if _, err := os.Stat(file2); os.IsNotExist(err) {
		fmt.Printf("❌ 文件不存在: %s\n", file2)
		os.Exit(1)
	}

	fmt.Println("🚀 Bakfu - 备份文件合并工具")
	fmt.Println(strings.Repeat("=", 50))

	// 创建合并器
	merger := NewBackupMerger(*autoResolve)
	tempDir := "temp-merge"

	// 清理函数
	defer func() {
		os.RemoveAll(tempDir)
	}()

	// 加载数据
	var data1, data2 *BackupData
	var err error

	// 加载第一个文件
	if strings.HasSuffix(strings.ToLower(file1), ".zip") {
		extractDir1 := filepath.Join(tempDir, "extract1")
		data1, err = merger.ExtractFromZip(file1, extractDir1)
	} else {
		data1, err = merger.LoadFromJSON(file1)
	}

	if err != nil {
		log.Fatalf("❌ 加载文件1失败: %v", err)
	}

	// 加载第二个文件
	if strings.HasSuffix(strings.ToLower(file2), ".zip") {
		extractDir2 := filepath.Join(tempDir, "extract2")
		data2, err = merger.ExtractFromZip(file2, extractDir2)
	} else {
		data2, err = merger.LoadFromJSON(file2)
	}

	if err != nil {
		log.Fatalf("❌ 加载文件2失败: %v", err)
	}

	// 显示文件信息
	fmt.Printf("\n📋 文件1信息:\n")
	fmt.Printf("  时间: %s\n", time.Unix(data1.Time/1000, 0).Format("2006-01-02 15:04:05"))
	fmt.Printf("  版本: %d\n", data1.Version)

	fmt.Printf("\n📋 文件2信息:\n")
	fmt.Printf("  时间: %s\n", time.Unix(data2.Time/1000, 0).Format("2006-01-02 15:04:05"))
	fmt.Printf("  版本: %d\n", data2.Version)

	// 合并数据
	mergedData, err := merger.MergeData(data1, data2)
	if err != nil {
		log.Fatalf("❌ 合并数据失败: %v", err)
	}

	// 保存结果
	outputFormat := *format
	if outputFormat == "" {
		// 根据扩展名自动判断
		if strings.HasSuffix(strings.ToLower(*output), ".json.gz") {
			outputFormat = "json.gz"
		} else if strings.HasSuffix(strings.ToLower(*output), ".json") {
			outputFormat = "json"
		} else {
			outputFormat = "zip"
		}
	}

	if err := merger.SaveBackup(mergedData, *output, outputFormat); err != nil {
		log.Fatalf("❌ 保存结果失败: %v", err)
	}

	// 打印摘要
	merger.PrintSummary()

	fmt.Printf("\n✅ 合并完成！输出文件: %s\n", *output)
}