package merge

import (
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// saveAsJSON 保存为JSON格式
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

// saveAsZip 保存为ZIP格式
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

// saveByExtension 根据文件扩展名自动判断格式
func (bm *BackupMerger) saveByExtension(data *BackupData, outputPath string) error {
	ext := strings.ToLower(filepath.Ext(outputPath))
	if ext == ".gz" || strings.HasSuffix(strings.ToLower(outputPath), ".json.gz") {
		return bm.saveAsJSON(data, outputPath, true)
	} else if ext == ".json" {
		return bm.saveAsJSON(data, outputPath, false)
	}
	return bm.saveAsZip(data, outputPath)
}

// NormalizePersistJSON normalizes persist JSON string
func NormalizePersistJSON(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}

	obj := make(map[string]interface{})
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", err
	}

	bytes, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func (bm *BackupMerger) extractPersistValue(input *BackupInput) (string, error) {
	switch input.Kind {
	case BackupKindLegacy:
		if input.LegacyData == nil || input.LegacyData.LocalStorage == nil {
			return "{}", nil
		}
		if raw, ok := input.LegacyData.LocalStorage["persist:cherry-studio"].(string); ok {
			return NormalizePersistJSON(raw)
		}
		return "{}", nil
	case BackupKindDirect:
		leveldbDir := filepath.Join(input.ExtractDir, "Local Storage", "leveldb")
		raw, err := ReadPersistCherryStudio(leveldbDir)
		if err != nil {
			return "", fmt.Errorf("读取新版备份 localStorage 失败: %v", err)
		}
		return NormalizePersistJSON(raw)
	default:
		return "", fmt.Errorf("不支持的输入类型: %s", input.Kind)
	}
}

func (bm *BackupMerger) mergePersistValues(v1, v2 string) (string, error) {
	map1 := make(map[string]interface{})
	map2 := make(map[string]interface{})

	if strings.TrimSpace(v1) != "" {
		if err := json.Unmarshal([]byte(v1), &map1); err != nil {
			return "", fmt.Errorf("解析第一个 persist:cherry-studio 失败: %v", err)
		}
	}

	if strings.TrimSpace(v2) != "" {
		if err := json.Unmarshal([]byte(v2), &map2); err != nil {
			return "", fmt.Errorf("解析第二个 persist:cherry-studio 失败: %v", err)
		}
	}

	merged, err := bm.MergeMaps(map1, map2, "localStorage.persist:cherry-studio")
	if err != nil {
		return "", err
	}

	bytes, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

// GetInputTimestamp 获取输入的时间戳
func GetInputTimestamp(input *BackupInput) int64 {
	if input == nil {
		return 0
	}
	if input.Kind == BackupKindLegacy && input.LegacyData != nil {
		return input.LegacyData.Time
	}
	if input.Kind == BackupKindDirect && input.Metadata != nil {
		return input.Metadata.Timestamp
	}
	return 0
}

func (bm *BackupMerger) choosePreferredInput(input1, input2 *BackupInput) *BackupInput {
	switch bm.AutoResolve {
	case "file2":
		return input2
	case "newer":
		if GetInputTimestamp(input2) > GetInputTimestamp(input1) {
			return input2
		}
		return input1
	case "older":
		if GetInputTimestamp(input1) <= GetInputTimestamp(input2) {
			return input1
		}
		return input2
	default:
		return input1
	}
}

func chooseAvailableDirectInput(preferred, fallback *BackupInput) *BackupInput {
	if preferred != nil && preferred.Kind == BackupKindDirect {
		return preferred
	}
	if fallback != nil && fallback.Kind == BackupKindDirect {
		return fallback
	}
	return nil
}

func maxInputTimestamp(input1, input2 *BackupInput) int64 {
	return MaxInt64(GetInputTimestamp(input1), GetInputTimestamp(input2))
}

// MergeDirectPractical 新版备份合并
func (bm *BackupMerger) MergeDirectPractical(input1, input2 *BackupInput) (*DirectMergeResult, error) {
	persist1, err := bm.extractPersistValue(input1)
	if err != nil {
		return nil, err
	}

	persist2, err := bm.extractPersistValue(input2)
	if err != nil {
		return nil, err
	}

	mergedPersist, err := bm.mergePersistValues(persist1, persist2)
	if err != nil {
		return nil, err
	}

	preferred := bm.choosePreferredInput(input1, input2)
	other := input2
	if preferred == input2 {
		other = input1
	}

	directBase := chooseAvailableDirectInput(preferred, other)
	if directBase == nil {
		return nil, fmt.Errorf("direct 合并需要至少一个新版备份输入")
	}

	metadata := DirectMetadata{
		Version:    6,
		Timestamp:  maxInputTimestamp(input1, input2),
		AppName:    "Cherry Studio",
		AppVersion: "unknown",
		Platform:   "",
		Arch:       "",
	}

	if directBase.Metadata != nil {
		metadata = *directBase.Metadata
		metadata.Timestamp = maxInputTimestamp(input1, input2)
		if metadata.Version < 6 {
			metadata.Version = 6
		}
	}

	// Collect Data directories from both inputs (preferred first for conflict resolution)
	var dataDirs []string
	preferredDataDir := filepath.Join(directBase.ExtractDir, "Data")
	if PathExists(preferredDataDir) {
		dataDirs = append(dataDirs, preferredDataDir)
	}
	// Also include the other input's Data directory
	otherInput := other
	if directBase == other {
		otherInput = preferred
	}
	otherDataDir := filepath.Join(otherInput.ExtractDir, "Data")
	if PathExists(otherDataDir) && otherDataDir != preferredDataDir {
		dataDirs = append(dataDirs, otherDataDir)
	}

	// Track legacy data.json for IndexedDB preservation
	var legacyDataJSON string
	var legacyInput *BackupInput
	if input1.Kind == BackupKindLegacy {
		legacyInput = input1
	} else if input2.Kind == BackupKindLegacy {
		legacyInput = input2
	}
	if legacyInput != nil && legacyInput.ExtractDir != "" {
		candidate := filepath.Join(legacyInput.ExtractDir, "data.json")
		if PathExists(candidate) {
			legacyDataJSON = candidate
		}
	}

	// Warn about IndexedDB limitation
	if legacyInput != nil && legacyInput.LegacyData != nil && legacyInput.LegacyData.IndexedDB != nil {
		totalItems := 0
		for table, val := range legacyInput.LegacyData.IndexedDB {
			if arr, ok := val.([]interface{}); ok {
				totalItems += len(arr)
				_ = table
			}
		}
		if totalItems > 0 {
			fmt.Printf("\n⚠️  注意: 旧版备份(data.json)中包含 IndexedDB 数据 (%d 条记录),\n", totalItems)
			fmt.Println("   由于新版备份使用 Chromium 二进制 IndexedDB 格式，无法自动合并。")
			fmt.Println("   旧版 data.json 将包含在输出中 (legacy-data.json) 以便手动恢复。")
		}
	}

	return &DirectMergeResult{
		Metadata:              metadata,
		MergedPersist:         mergedPersist,
		LocalStorageSourceDir: filepath.Join(directBase.ExtractDir, "Local Storage"),
		IndexedDBSourceDir:    filepath.Join(directBase.ExtractDir, "IndexedDB"),
		DataSourceDirs:        dataDirs,
		LegacyDataJSON:        legacyDataJSON,
	}, nil
}

// SaveDirectBackup 保存新版合并结果
func (bm *BackupMerger) SaveDirectBackup(result *DirectMergeResult, outputPath string) error {
	fmt.Printf("💾 保存新版合并结果: %s\n", outputPath)

	if err := MkdirAll(outputPath); err != nil {
		return fmt.Errorf("创建输出目录失败: %v", err)
	}

	tempRoot, err := os.MkdirTemp("", "bakfu-direct-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)

	localStorageDst := filepath.Join(tempRoot, "Local Storage")
	if !PathExists(result.LocalStorageSourceDir) {
		return fmt.Errorf("新版备份缺少 Local Storage 目录: %s", result.LocalStorageSourceDir)
	}
	if err := CopyDir(result.LocalStorageSourceDir, localStorageDst); err != nil {
		return fmt.Errorf("复制 Local Storage 目录失败: %v", err)
	}

	if PathExists(result.IndexedDBSourceDir) {
		if err := CopyDir(result.IndexedDBSourceDir, filepath.Join(tempRoot, "IndexedDB")); err != nil {
			return fmt.Errorf("复制 IndexedDB 目录失败: %v", err)
		}
	}

	// Merge Data directories: copy in reverse order so preferred (first) wins on conflict
	dataDst := filepath.Join(tempRoot, "Data")
	for i := len(result.DataSourceDirs) - 1; i >= 0; i-- {
		srcDir := result.DataSourceDirs[i]
		if PathExists(srcDir) {
			if err := CopyDir(srcDir, dataDst); err != nil {
				return fmt.Errorf("复制 Data 目录失败 (%s): %v", srcDir, err)
			}
		}
	}

	metadataPath := filepath.Join(tempRoot, "metadata.json")
	metaBytes, err := json.MarshalIndent(result.Metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(metadataPath, metaBytes, 0644); err != nil {
		return err
	}

	leveldbDir := filepath.Join(localStorageDst, "leveldb")
	if !PathExists(leveldbDir) {
		return fmt.Errorf("Local Storage 缺少 leveldb 目录: %s", leveldbDir)
	}
	if err := WritePersistCherryStudio(leveldbDir, result.MergedPersist); err != nil {
		return fmt.Errorf("回写 persist:cherry-studio 失败: %v", err)
	}

	// Include legacy data.json for IndexedDB preservation
	if result.LegacyDataJSON != "" {
		dstPath := filepath.Join(tempRoot, "legacy-data.json")
		if err := CopyFile(result.LegacyDataJSON, dstPath); err != nil {
			return fmt.Errorf("复制 legacy-data.json 失败: %v", err)
		}
	}

	if err := ZipDirectory(tempRoot, outputPath); err != nil {
		return fmt.Errorf("打包 direct ZIP 失败: %v", err)
	}

	return nil
}
