package merge

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// PrintValue 打印值
func (bm *BackupMerger) PrintValue(value interface{}) {
	// If it's a JSON string, parse and display the inner structure
	if parsed := TryParseJSON(value); parsed != nil {
		bm.printParsedValue(parsed)
		return
	}

	if valueMap, ok := value.(map[string]interface{}); ok {
		bm.printMapValue(valueMap)
	} else {
		fmt.Printf("  %v\n", FormatValueLong(value))
	}
}

func (bm *BackupMerger) printParsedValue(value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		bm.printMapValue(v)
	case []interface{}:
		fmt.Printf("  [数组: %d 项]\n", len(v))
		for i, item := range v {
			if i >= 5 {
				fmt.Printf("  ... (共 %d 项)\n", len(v))
				break
			}
			fmt.Printf("  [%d]: %s\n", i, FormatValueLong(item))
		}
	default:
		fmt.Printf("  %s\n", FormatValueLong(value))
	}
}

func (bm *BackupMerger) printMapValue(m map[string]interface{}) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	count := 0
	for _, key := range keys {
		if count >= 8 {
			fmt.Printf("  ... (共 %d 个字段)\n", len(m))
			break
		}
		fmt.Printf("  %s: %s\n", key, FormatValueLong(m[key]))
		count++
	}
}

// TryParseJSON attempts to parse a string as JSON, returning the parsed value or nil.
func TryParseJSON(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return nil
	}
	return parsed
}

// ShowDiff 显示差异
func (bm *BackupMerger) ShowDiff(value1, value2 interface{}) {
	fmt.Println("\n🔍 详细差异:")

	// If both values are JSON strings, parse and compare the inner structure
	if parsed1 := TryParseJSON(value1); parsed1 != nil {
		if parsed2 := TryParseJSON(value2); parsed2 != nil {
			bm.showDiffParsed(parsed1, parsed2)
			return
		}
	}

	if map1, ok1 := value1.(map[string]interface{}); ok1 {
		if map2, ok2 := value2.(map[string]interface{}); ok2 {
			bm.showMapDiff(map1, map2)
			return
		}
	}

	// Fallback: show both values directly
	fmt.Printf("\n  文件1: %s\n", FormatValueLong(value1))
	fmt.Printf("  文件2: %s\n", FormatValueLong(value2))
}

func (bm *BackupMerger) showDiffParsed(v1, v2 interface{}) {
	map1, ok1 := v1.(map[string]interface{})
	map2, ok2 := v2.(map[string]interface{})
	if ok1 && ok2 {
		bm.showMapDiff(map1, map2)
		return
	}

	fmt.Printf("\n  文件1: %s\n", FormatValueLong(v1))
	fmt.Printf("  文件2: %s\n", FormatValueLong(v2))
}

func (bm *BackupMerger) showMapDiff(map1, map2 map[string]interface{}) {
	keys := make(map[string]bool)
	for key := range map1 {
		keys[key] = true
	}
	for key := range map2 {
		keys[key] = true
	}

	// Sort keys for stable output
	sortedKeys := make([]string, 0, len(keys))
	for key := range keys {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Strings(sortedKeys)

	hasDiff := false
	for _, key := range sortedKeys {
		val1, in1 := map1[key]
		val2, in2 := map2[key]

		if !in1 {
			fmt.Printf("\n  %s:\n", key)
			fmt.Printf("    文件1: (不存在)\n")
			fmt.Printf("    文件2: %s\n", FormatValueLong(val2))
			hasDiff = true
		} else if !in2 {
			fmt.Printf("\n  %s:\n", key)
			fmt.Printf("    文件1: %s\n", FormatValueLong(val1))
			fmt.Printf("    文件2: (不存在)\n")
			hasDiff = true
		} else if !reflect.DeepEqual(val1, val2) {
			fmt.Printf("\n  %s:\n", key)
			fmt.Printf("    文件1: %s\n", FormatValueLong(val1))
			fmt.Printf("    文件2: %s\n", FormatValueLong(val2))
			hasDiff = true
		}
	}

	if !hasDiff {
		fmt.Println("  (无差异)")
	}
}

// FormatValueLong formats a value with truncation for long strings
func FormatValueLong(value interface{}) string {
	if value == nil {
		return "null"
	}
	switch v := value.(type) {
	case string:
		if len(v) > 200 {
			return v[:200] + "..."
		}
		return v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		s := string(b)
		if len(s) > 200 {
			return s[:200] + "..."
		}
		return s
	case []interface{}:
		b, _ := json.Marshal(v)
		s := string(b)
		if len(s) > 200 {
			return s[:200] + "..."
		}
		return s
	default:
		s := fmt.Sprintf("%v", v)
		if len(s) > 200 {
			return s[:200] + "..."
		}
		return s
	}
}

// PrintSummary 打印摘要
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

// GetItemID 从 item 获取 ID
func GetItemID(item map[string]interface{}, index int) string {
	if id, ok := item["id"].(string); ok {
		return id
	}
	return fmt.Sprintf("__index_%d", index)
}

// GetConflictType 获取冲突类型标签
func GetConflictType(tableName string) string {
	if conflictType, ok := ConflictTypes[tableName]; ok {
		return conflictType
	}
	return tableName
}

// GetConflictTypeFromContext 从 context 路径推断冲突类型
func GetConflictTypeFromContext(context string) string {
	priority := []string{"providers", "models", "assistants", "settings", "knowledge", "mcp_servers", "prompts", "localStorage"}
	for _, key := range priority {
		if key == "localStorage" && context != "localStorage" {
			continue
		}
		if strings.Contains(context, key) {
			if label, ok := ConflictTypes[key]; ok {
				return label
			}
		}
	}
	return context
}

// HasIDField 检查数组中是否存在带 id 字段的对象
func HasIDField(arr []interface{}) bool {
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			if _, hasID := m["id"]; hasID {
				return true
			}
		}
	}
	return false
}

// GetTimestamp 从值中获取时间戳
func GetTimestamp(value interface{}) int64 {
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

// FormatValue 格式化值（短版本）
func FormatValue(value interface{}) string {
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

// MaxInt64 returns the max of two int64 values
func MaxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// MaxInt returns the max of two int values
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// PathExists checks if path exists
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MkdirAll creates directory for file path
func MkdirAll(outputPath string) error {
	return os.MkdirAll(filepath.Dir(outputPath), 0755)
}

// CopyFile copies a single file
func CopyFile(srcPath, dstPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// CopyDir copies directory recursively
func CopyDir(srcDir, dstDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("不是目录: %s", srcDir)
	}

	if err := os.MkdirAll(dstDir, info.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())
		if entry.IsDir() {
			if err := CopyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := CopyFile(srcPath, dstPath); err != nil {
			return err
		}
	}

	return nil
}

// ZipDirectory zips a directory
func ZipDirectory(srcDir, outputZip string) error {
	outFile, err := os.Create(outputZip)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		zipPath := filepath.ToSlash(relPath)
		if info.IsDir() {
			_, err := zipWriter.Create(zipPath + "/")
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = zipPath
		header.Method = zip.Deflate

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		_, err = io.Copy(writer, srcFile)
		return err
	})
}
