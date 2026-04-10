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

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// 备份数据结构
type BackupData struct {
	Time         int64                  `json:"time"`
	Version      int                    `json:"version"`
	LocalStorage map[string]interface{} `json:"localStorage"`
	IndexedDB    map[string]interface{} `json:"indexedDB"`
}

type BackupKind string

const (
	BackupKindLegacy BackupKind = "legacy"
	BackupKindDirect BackupKind = "direct"
)

type DirectMetadata struct {
	Version    int    `json:"version"`
	Timestamp  int64  `json:"timestamp"`
	AppName    string `json:"appName"`
	AppVersion string `json:"appVersion"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
}

type BackupInput struct {
	Kind       BackupKind
	LegacyData *BackupData
	ExtractDir string
	Metadata   *DirectMetadata
}

type DirectMergeResult struct {
	Metadata              DirectMetadata
	MergedPersist         string
	LocalStorageSourceDir string
	IndexedDBSourceDir    string
	DataSourceDirs        []string // multiple Data dirs to merge (preferred first)
	LegacyDataJSON        string   // path to legacy data.json if one input was legacy (for IndexedDB preservation)
}

// 冲突类型
var ConflictTypes = map[string]string{
	"providers":    "AI服务提供商",
	"models":       "模型配置",
	"assistants":   "助手配置",
	"settings":     "应用设置",
	"knowledge":    "知识库",
	"mcp_servers":  "MCP服务器",
	"prompts":      "提示词",
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

// 从ZIP文件提取备份数据（兼容 legacy 和 direct）
func (bm *BackupMerger) ExtractFromZip(zipPath, extractDir string) (*BackupInput, error) {
	fmt.Printf("📦 解压备份文件: %s\n", zipPath)

	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return nil, fmt.Errorf("创建解压目录失败: %v", err)
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("打开ZIP文件失败: %v", err)
	}
	defer r.Close()

	var hasDataJSON, hasMetadata, hasIndexedDB, hasLocalStorage bool

	for _, f := range r.File {
		cleanName := filepath.ToSlash(filepath.Clean(f.Name))
		switch {
		case cleanName == "data.json":
			hasDataJSON = true
		case cleanName == "metadata.json":
			hasMetadata = true
		case strings.HasPrefix(cleanName, "IndexedDB/"):
			hasIndexedDB = true
		case strings.HasPrefix(cleanName, "Local Storage/"):
			hasLocalStorage = true
		}

		extractPath, err := bm.safeExtractPath(extractDir, f.Name)
		if err != nil {
			return nil, fmt.Errorf("非法ZIP路径 %s: %v", f.Name, err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(extractPath, f.FileInfo().Mode()); err != nil {
				return nil, fmt.Errorf("创建目录 %s 失败: %v", f.Name, err)
			}
			continue
		}

		if err := bm.extractFile(f, extractPath); err != nil {
			return nil, fmt.Errorf("解压文件 %s 失败: %v", f.Name, err)
		}
	}

	if hasDataJSON {
		dataPath := filepath.Join(extractDir, "data.json")
		file, err := os.Open(dataPath)
		if err != nil {
			return nil, fmt.Errorf("打开data.json失败: %v", err)
		}
		defer file.Close()

		var data BackupData
		if err := json.NewDecoder(file).Decode(&data); err != nil {
			return nil, fmt.Errorf("解析data.json失败: %v", err)
		}

		return &BackupInput{
			Kind:       BackupKindLegacy,
			LegacyData: &data,
			ExtractDir: extractDir,
		}, nil
	}

	if hasMetadata {
		if !hasIndexedDB || !hasLocalStorage {
			return nil, fmt.Errorf("检测到新版备份(metadata.json)，但缺少必要目录: IndexedDB=%v, Local Storage=%v", hasIndexedDB, hasLocalStorage)
		}

		metadataPath := filepath.Join(extractDir, "metadata.json")
		metaFile, err := os.Open(metadataPath)
		if err != nil {
			return nil, fmt.Errorf("打开metadata.json失败: %v", err)
		}
		defer metaFile.Close()

		var metadata DirectMetadata
		if err := json.NewDecoder(metaFile).Decode(&metadata); err != nil {
			return nil, fmt.Errorf("解析metadata.json失败: %v", err)
		}

		return &BackupInput{
			Kind:       BackupKindDirect,
			ExtractDir: extractDir,
			Metadata:   &metadata,
		}, nil
	}

	return nil, fmt.Errorf("无法识别ZIP备份格式: 既没有 data.json，也没有 metadata.json")
}

func (bm *BackupMerger) safeExtractPath(baseDir, zipName string) (string, error) {
	targetPath := filepath.Join(baseDir, filepath.Clean(zipName))
	relPath, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("路径越界")
	}
	return targetPath, nil
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

// Chromium LocalStorage leveldb key format: "_" + origin + "\x00\x01" + key
var chromiumPersistKey = []byte("_file://\x00\x01persist:cherry-studio")
var simplePersistKey = []byte("persist:cherry-studio")

func readPersistCherryStudio(leveldbDir string) (string, error) {
	db, err := leveldb.OpenFile(leveldbDir, &opt.Options{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer db.Close()

	// Try Chromium format key first
	value, err := db.Get(chromiumPersistKey, nil)
	if err == nil {
		return decodeLevelDBValue(value), nil
	}

	// Fall back to simple key (used in tests and older bakfu output)
	value, err = db.Get(simplePersistKey, nil)
	if err == leveldb.ErrNotFound {
		return "{}", nil
	}
	if err != nil {
		return "", err
	}

	return string(value), nil
}

// decodeLevelDBValue decodes a Chromium LocalStorage leveldb value.
// Chromium stores values with a 1-byte prefix: 0x00 for UTF-16LE, 0x01 for Latin-1.
func decodeLevelDBValue(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}

	encoding := raw[0]
	payload := raw[1:]

	if encoding == 0x00 {
		// UTF-16LE encoded
		return decodeUTF16LE(payload)
	}
	// Latin-1 (0x01) or unknown: treat as raw bytes
	return string(payload)
}

func decodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		r := rune(b[i]) | rune(b[i+1])<<8
		runes = append(runes, r)
	}
	return string(runes)
}

func encodeUTF16LE(s string) []byte {
	runes := []rune(s)
	buf := make([]byte, len(runes)*2)
	for i, r := range runes {
		buf[i*2] = byte(r & 0xFF)
		buf[i*2+1] = byte((r >> 8) & 0xFF)
	}
	return buf
}

func writePersistCherryStudio(leveldbDir, value string) error {
	db, err := leveldb.OpenFile(leveldbDir, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	// Check if chromium key exists; if so, write in chromium format
	_, err = db.Get(chromiumPersistKey, nil)
	if err == nil {
		// Encode as UTF-16LE with 0x00 prefix (Chromium format)
		encoded := append([]byte{0x00}, encodeUTF16LE(value)...)
		return db.Put(chromiumPersistKey, encoded, nil)
	}

	// No chromium key exists — write with simple key
	return db.Put(simplePersistKey, []byte(value), nil)
}

func normalizePersistJSON(raw string) (string, error) {
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
			return normalizePersistJSON(raw)
		}
		return "{}", nil
	case BackupKindDirect:
		leveldbDir := filepath.Join(input.ExtractDir, "Local Storage", "leveldb")
		raw, err := readPersistCherryStudio(leveldbDir)
		if err != nil {
			return "", fmt.Errorf("读取新版备份 localStorage 失败: %v", err)
		}
		return normalizePersistJSON(raw)
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

	merged, err := bm.mergeMaps(map1, map2, "localStorage.persist:cherry-studio")
	if err != nil {
		return "", err
	}

	bytes, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func getInputTimestamp(input *BackupInput) int64 {
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
		if getInputTimestamp(input2) > getInputTimestamp(input1) {
			return input2
		}
		return input1
	case "older":
		if getInputTimestamp(input1) <= getInputTimestamp(input2) {
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
	return maxInt64(getInputTimestamp(input1), getInputTimestamp(input2))
}

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
	if pathExists(preferredDataDir) {
		dataDirs = append(dataDirs, preferredDataDir)
	}
	// Also include the other input's Data directory
	otherInput := other
	if directBase == other {
		otherInput = preferred
	}
	otherDataDir := filepath.Join(otherInput.ExtractDir, "Data")
	if pathExists(otherDataDir) && otherDataDir != preferredDataDir {
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
		if pathExists(candidate) {
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

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(srcPath, dstPath string) error {
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

func copyDir(srcDir, dstDir string) error {
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
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}

	return nil
}

func zipDirectory(srcDir, outputZip string) error {
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

func (bm *BackupMerger) SaveDirectBackup(result *DirectMergeResult, outputPath string) error {
	fmt.Printf("💾 保存新版合并结果: %s\n", outputPath)

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %v", err)
	}

	tempRoot, err := os.MkdirTemp("", "bakfu-direct-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempRoot)

	localStorageDst := filepath.Join(tempRoot, "Local Storage")
	if !pathExists(result.LocalStorageSourceDir) {
		return fmt.Errorf("新版备份缺少 Local Storage 目录: %s", result.LocalStorageSourceDir)
	}
	if err := copyDir(result.LocalStorageSourceDir, localStorageDst); err != nil {
		return fmt.Errorf("复制 Local Storage 目录失败: %v", err)
	}

	if pathExists(result.IndexedDBSourceDir) {
		if err := copyDir(result.IndexedDBSourceDir, filepath.Join(tempRoot, "IndexedDB")); err != nil {
			return fmt.Errorf("复制 IndexedDB 目录失败: %v", err)
		}
	}

	// Merge Data directories: copy in reverse order so preferred (first) wins on conflict
	dataDst := filepath.Join(tempRoot, "Data")
	for i := len(result.DataSourceDirs) - 1; i >= 0; i-- {
		srcDir := result.DataSourceDirs[i]
		if pathExists(srcDir) {
			if err := copyDir(srcDir, dataDst); err != nil {
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
	if !pathExists(leveldbDir) {
		return fmt.Errorf("Local Storage 缺少 leveldb 目录: %s", leveldbDir)
	}
	if err := writePersistCherryStudio(leveldbDir, result.MergedPersist); err != nil {
		return fmt.Errorf("回写 persist:cherry-studio 失败: %v", err)
	}

	// Include legacy data.json for IndexedDB preservation
	if result.LegacyDataJSON != "" {
		dstPath := filepath.Join(tempRoot, "legacy-data.json")
		if err := copyFile(result.LegacyDataJSON, dstPath); err != nil {
			return fmt.Errorf("复制 legacy-data.json 失败: %v", err)
		}
	}

	if err := zipDirectory(tempRoot, outputPath); err != nil {
		return fmt.Errorf("打包 direct ZIP 失败: %v", err)
	}

	return nil
}

// 合并两个备份数据
func (bm *BackupMerger) MergeData(data1, data2 *BackupData) (*BackupData, error) {
	fmt.Println("\n🔄 开始合并数据...")

	merged := &BackupData{
		Time:         maxInt64(data1.Time, data2.Time),
		Version:      maxInt(data1.Version, data2.Version),
		LocalStorage: make(map[string]interface{}),
		IndexedDB:    make(map[string]interface{}),
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

// 合并对象数组（按 ID 合并，同 ID 不同内容两个都保留）
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
		merged = append(merged, item1)
		if item2, exists := map2[id]; exists {
			if !reflect.DeepEqual(item1, item2) {
				// Same ID, different content: keep both, rename the second copy
				dup := duplicateWithNewID(item2, "(文件2)")
				merged = append(merged, dup)
			}
			// Same ID, same content: already added item1, skip
		}
	}
	for id, item2 := range map2 {
		if !processedIds[id] {
			merged = append(merged, item2)
		}
	}

	return merged, nil
}

// duplicateWithNewID clones a map item, assigns a new ID, and appends a suffix to name/title fields.
func duplicateWithNewID(item interface{}, suffix string) interface{} {
	itemMap, ok := item.(map[string]interface{})
	if !ok {
		return item
	}

	dup := make(map[string]interface{}, len(itemMap))
	for k, v := range itemMap {
		dup[k] = v
	}

	// Generate a new unique ID
	if origID, ok := dup["id"].(string); ok {
		dup["id"] = origID + "_" + generateShortID()
	}

	// Append suffix to display name fields
	for _, field := range []string{"name", "title"} {
		if name, ok := dup[field].(string); ok && name != "" {
			dup[field] = name + " " + suffix
		}
	}

	return dup
}

// generateShortID returns a short random-ish ID based on current time.
func generateShortID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFF)
}

// 合并单个值：智能分发数组/对象/标量
func (bm *BackupMerger) mergeValue(value1, value2 interface{}, context string) (interface{}, error) {
	if reflect.DeepEqual(value1, value2) {
		return value1, nil
	}

	// If both are JSON strings, parse and merge the inner structure
	str1, isStr1 := value1.(string)
	str2, isStr2 := value2.(string)
	if isStr1 && isStr2 {
		var parsed1, parsed2 interface{}
		err1 := json.Unmarshal([]byte(str1), &parsed1)
		err2 := json.Unmarshal([]byte(str2), &parsed2)
		if err1 == nil && err2 == nil {
			merged, err := bm.mergeValue(parsed1, parsed2, context)
			if err != nil {
				return nil, err
			}
			// Re-serialize back to JSON string
			b, err := json.Marshal(merged)
			if err != nil {
				return nil, err
			}
			return string(b), nil
		}
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
	// If it's a JSON string, parse and display the inner structure
	if parsed := tryParseJSON(value); parsed != nil {
		bm.printParsedValue(parsed)
		return
	}

	if valueMap, ok := value.(map[string]interface{}); ok {
		bm.printMapValue(valueMap)
	} else {
		fmt.Printf("  %v\n", formatValueLong(value))
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
			fmt.Printf("  [%d]: %s\n", i, formatValueLong(item))
		}
	default:
		fmt.Printf("  %s\n", formatValueLong(value))
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
		fmt.Printf("  %s: %s\n", key, formatValueLong(m[key]))
		count++
	}
}

// tryParseJSON attempts to parse a string as JSON, returning the parsed value or nil.
func tryParseJSON(v interface{}) interface{} {
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

// 显示差异
func (bm *BackupMerger) showDiff(value1, value2 interface{}) {
	fmt.Println("\n🔍 详细差异:")

	// If both values are JSON strings, parse and compare the inner structure
	if parsed1 := tryParseJSON(value1); parsed1 != nil {
		if parsed2 := tryParseJSON(value2); parsed2 != nil {
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
	fmt.Printf("\n  文件1: %s\n", formatValueLong(value1))
	fmt.Printf("  文件2: %s\n", formatValueLong(value2))
}

func (bm *BackupMerger) showDiffParsed(v1, v2 interface{}) {
	map1, ok1 := v1.(map[string]interface{})
	map2, ok2 := v2.(map[string]interface{})
	if ok1 && ok2 {
		bm.showMapDiff(map1, map2)
		return
	}

	fmt.Printf("\n  文件1: %s\n", formatValueLong(v1))
	fmt.Printf("  文件2: %s\n", formatValueLong(v2))
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
			fmt.Printf("    文件2: %s\n", formatValueLong(val2))
			hasDiff = true
		} else if !in2 {
			fmt.Printf("\n  %s:\n", key)
			fmt.Printf("    文件1: %s\n", formatValueLong(val1))
			fmt.Printf("    文件2: (不存在)\n")
			hasDiff = true
		} else if !reflect.DeepEqual(val1, val2) {
			fmt.Printf("\n  %s:\n", key)
			fmt.Printf("    文件1: %s\n", formatValueLong(val1))
			fmt.Printf("    文件2: %s\n", formatValueLong(val2))
			hasDiff = true
		}
	}

	if !hasDiff {
		fmt.Println("  (无差异)")
	}
}

func formatValueLong(value interface{}) string {
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
	var input1, input2 *BackupInput
	var err error

	// 加载第一个文件
	if strings.HasSuffix(strings.ToLower(file1), ".zip") {
		extractDir1 := filepath.Join(tempDir, "extract1")
		input1, err = merger.ExtractFromZip(file1, extractDir1)
	} else {
		data, loadErr := merger.LoadFromJSON(file1)
		err = loadErr
		if loadErr == nil {
			input1 = &BackupInput{Kind: BackupKindLegacy, LegacyData: data}
		}
	}

	if err != nil {
		log.Fatalf("❌ 加载文件1失败: %v", err)
	}

	// 加载第二个文件
	if strings.HasSuffix(strings.ToLower(file2), ".zip") {
		extractDir2 := filepath.Join(tempDir, "extract2")
		input2, err = merger.ExtractFromZip(file2, extractDir2)
	} else {
		data, loadErr := merger.LoadFromJSON(file2)
		err = loadErr
		if loadErr == nil {
			input2 = &BackupInput{Kind: BackupKindLegacy, LegacyData: data}
		}
	}

	if err != nil {
		log.Fatalf("❌ 加载文件2失败: %v", err)
	}

	printInputInfo := func(label string, input *BackupInput) {
		fmt.Printf("\n📋 %s信息:\n", label)
		switch input.Kind {
		case BackupKindLegacy:
			if input.LegacyData != nil {
				fmt.Printf("  类型: legacy(data.json)\n")
				fmt.Printf("  时间: %s\n", time.Unix(input.LegacyData.Time/1000, 0).Format("2006-01-02 15:04:05"))
				fmt.Printf("  版本: %d\n", input.LegacyData.Version)
			}
		case BackupKindDirect:
			fmt.Printf("  类型: direct(metadata.json)\n")
			if input.Metadata != nil {
				fmt.Printf("  时间: %s\n", time.Unix(input.Metadata.Timestamp/1000, 0).Format("2006-01-02 15:04:05"))
				fmt.Printf("  版本: %d\n", input.Metadata.Version)
			}
		default:
			fmt.Printf("  类型: unknown\n")
		}
	}

	printInputInfo("文件1", input1)
	printInputInfo("文件2", input2)

	// 保存结果
	outputFormat := *format
	if outputFormat == "" {
		if strings.HasSuffix(strings.ToLower(*output), ".json.gz") {
			outputFormat = "json.gz"
		} else if strings.HasSuffix(strings.ToLower(*output), ".json") {
			outputFormat = "json"
		} else {
			outputFormat = "zip"
		}
	}

	if input1.Kind == BackupKindLegacy && input2.Kind == BackupKindLegacy {
		mergedData, mergeErr := merger.MergeData(input1.LegacyData, input2.LegacyData)
		if mergeErr != nil {
			log.Fatalf("❌ 合并数据失败: %v", mergeErr)
		}
		if err := merger.SaveBackup(mergedData, *output, outputFormat); err != nil {
			log.Fatalf("❌ 保存结果失败: %v", err)
		}
	} else {
		if outputFormat != "zip" {
			log.Fatalf("❌ 包含新版备份时仅支持 ZIP 输出，请使用 -format zip 或 .zip 输出文件")
		}
		directResult, mergeErr := merger.MergeDirectPractical(input1, input2)
		if mergeErr != nil {
			log.Fatalf("❌ 合并新版备份失败: %v", mergeErr)
		}
		if err := merger.SaveDirectBackup(directResult, *output); err != nil {
			log.Fatalf("❌ 保存新版合并结果失败: %v", err)
		}
	}

	// 打印摘要
	merger.PrintSummary()

	fmt.Printf("\n✅ 合并完成！输出文件: %s\n", *output)
}
