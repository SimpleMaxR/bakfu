package merge

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

// ─── 辅助函数 ────────────────────────────────────────────────────────────────

func newMerger() *BackupMerger {
	return NewBackupMerger("file1") // 自动解决，避免测试时等待交互输入
}

// 从 []interface{} 中按 id 查找对象
func findByID(arr []interface{}, id string) map[string]interface{} {
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			if m["id"] == id {
				return m
			}
		}
	}
	return nil
}

// 收集数组中所有 id
func collectIDs(arr []interface{}) []string {
	var ids []string
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			if id, ok := m["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// ─── HasIDField ───────────────────────────────────────────────────────────────

func TestHasIDField_WithID(t *testing.T) {
	arr := []interface{}{
		map[string]interface{}{"id": "a", "name": "A"},
	}
	if !HasIDField(arr) {
		t.Error("期望返回 true，但返回了 false")
	}
}

func TestHasIDField_WithoutID(t *testing.T) {
	arr := []interface{}{
		map[string]interface{}{"name": "A"},
	}
	if HasIDField(arr) {
		t.Error("期望返回 false，但返回了 true")
	}
}

func TestHasIDField_Empty(t *testing.T) {
	if HasIDField([]interface{}{}) {
		t.Error("空数组期望返回 false")
	}
}

// ─── GetConflictTypeFromContext ───────────────────────────────────────────────

func TestGetConflictTypeFromContext_KnownKey(t *testing.T) {
	result := GetConflictTypeFromContext("localStorage.providers[openai]")
	if result != "AI服务提供商" {
		t.Errorf("期望 'AI服务提供商'，得到 '%s'", result)
	}
}

func TestGetConflictTypeFromContext_UnknownKey(t *testing.T) {
	ctx := "localStorage.unknownField"
	result := GetConflictTypeFromContext(ctx)
	if result != ctx {
		t.Errorf("未知路径应原样返回，得到 '%s'", result)
	}
}

// ─── MergeArrays ─────────────────────────────────────────────────────────────

// 两个文件各自独有的条目都应保留
func TestMergeArrays_UniqueIDsFromBothFiles(t *testing.T) {
	bm := newMerger()
	arr1 := []interface{}{
		map[string]interface{}{"id": "openai", "name": "OpenAI"},
	}
	arr2 := []interface{}{
		map[string]interface{}{"id": "claude", "name": "Claude"},
	}

	merged, err := bm.MergeArrays(arr1, arr2, "providers")
	if err != nil {
		t.Fatalf("MergeArrays 失败: %v", err)
	}

	ids := collectIDs(merged)
	if !reflect.DeepEqual(ids, []string{"claude", "openai"}) {
		t.Errorf("期望包含 [claude openai]，得到 %v", ids)
	}
}

// 相同 ID、内容相同 → 只保留一条，不产生冲突
func TestMergeArrays_SameIDSameContent(t *testing.T) {
	bm := newMerger()
	item := map[string]interface{}{"id": "openai", "name": "OpenAI"}
	arr1 := []interface{}{item}
	arr2 := []interface{}{item}

	merged, err := bm.MergeArrays(arr1, arr2, "providers")
	if err != nil {
		t.Fatalf("MergeArrays 失败: %v", err)
	}
	if len(merged) != 1 {
		t.Errorf("相同内容应去重，期望 1 条，得到 %d 条", len(merged))
	}
}

// 相同 ID、内容不同 → 两个都保留，第二个重命名
func TestMergeArrays_SameIDConflict_KeepBoth(t *testing.T) {
	bm := newMerger()
	arr1 := []interface{}{
		map[string]interface{}{"id": "openai", "name": "OpenAI", "apiKey": "old-key"},
	}
	arr2 := []interface{}{
		map[string]interface{}{"id": "openai", "name": "OpenAI Pro", "apiKey": "new-key"},
	}

	merged, err := bm.MergeArrays(arr1, arr2, "providers")
	if err != nil {
		t.Fatalf("MergeArrays 失败: %v", err)
	}

	if len(merged) != 2 {
		t.Fatalf("同 ID 不同内容应保留两个，期望 2 条，得到 %d 条", len(merged))
	}

	// 第一个是 file1 原样
	item1 := findByID(merged, "openai")
	if item1 == nil {
		t.Fatal("未找到原始 id=openai 的条目")
	}
	if item1["apiKey"] != "old-key" {
		t.Errorf("原始条目应保持 file1 的值，得到 apiKey=%v", item1["apiKey"])
	}

	// 第二个是 file2 的副本，ID 已改、name 带后缀
	var item2 map[string]interface{}
	for _, m := range merged {
		if mm, ok := m.(map[string]interface{}); ok {
			if id, _ := mm["id"].(string); id != "openai" && strings.HasPrefix(id, "openai_") {
				item2 = mm
				break
			}
		}
	}
	if item2 == nil {
		t.Fatal("未找到重命名后的 file2 副本")
	}
	if item2["apiKey"] != "new-key" {
		t.Errorf("副本应保持 file2 的值，得到 apiKey=%v", item2["apiKey"])
	}
	if name, _ := item2["name"].(string); !strings.Contains(name, "(文件2)") {
		t.Errorf("副本 name 应含 '(文件2)'，得到 '%s'", name)
	}
}

// file1 有 A，file2 有 A(不同)+B → 结果应有 A(file1) + A(file2 副本) + B
func TestMergeArrays_MixedIDsWithConflict(t *testing.T) {
	bm := newMerger()
	arr1 := []interface{}{
		map[string]interface{}{"id": "openai", "name": "OpenAI"},
	}
	arr2 := []interface{}{
		map[string]interface{}{"id": "openai", "name": "OpenAI Pro"},
		map[string]interface{}{"id": "gemini", "name": "Gemini"},
	}

	merged, err := bm.MergeArrays(arr1, arr2, "providers")
	if err != nil {
		t.Fatalf("MergeArrays 失败: %v", err)
	}

	// Should have 3 items: openai(file1) + openai_xxx(file2) + gemini
	if len(merged) != 3 {
		t.Errorf("期望 3 条(两个 openai + gemini)，得到 %d 条", len(merged))
	}

	// gemini should be present
	if findByID(merged, "gemini") == nil {
		t.Error("应包含 gemini")
	}
	// original openai should be present
	if findByID(merged, "openai") == nil {
		t.Error("应包含原始 openai")
	}
}

// ─── MergeValue ──────────────────────────────────────────────────────────────

// 相同值直接返回，不触发冲突
func TestMergeValue_EqualValues(t *testing.T) {
	bm := newMerger()
	result, err := bm.MergeValue("hello", "hello", "ctx")
	if err != nil {
		t.Fatalf("MergeValue 失败: %v", err)
	}
	if result != "hello" {
		t.Errorf("相同值应原样返回，得到 %v", result)
	}
}

// 两个带 ID 的数组 → 走 MergeArrays 路径（按 ID 合并）
func TestMergeValue_ArraysWithID(t *testing.T) {
	bm := newMerger()
	v1 := []interface{}{
		map[string]interface{}{"id": "a", "x": 1},
	}
	v2 := []interface{}{
		map[string]interface{}{"id": "b", "x": 2},
	}

	result, err := bm.MergeValue(v1, v2, "ctx")
	if err != nil {
		t.Fatalf("MergeValue 失败: %v", err)
	}

	arr, ok := result.([]interface{})
	if !ok {
		t.Fatal("结果应为 []interface{}")
	}
	ids := collectIDs(arr)
	if !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Errorf("期望 [a b]，得到 %v", ids)
	}
}

// 两个 map → 走 MergeMaps 路径（递归合并）
func TestMergeValue_Maps(t *testing.T) {
	bm := newMerger()
	v1 := map[string]interface{}{"theme": "dark", "lang": "zh"}
	v2 := map[string]interface{}{"theme": "light", "fontSize": 14}

	result, err := bm.MergeValue(v1, v2, "settings")
	if err != nil {
		t.Fatalf("MergeValue 失败: %v", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("结果应为 map[string]interface{}")
	}
	// fontSize 只在 v2，应保留
	if m["fontSize"] != 14 {
		t.Errorf("期望 fontSize=14，得到 %v", m["fontSize"])
	}
	// lang 只在 v1，应保留
	if m["lang"] != "zh" {
		t.Errorf("期望 lang=zh，得到 %v", m["lang"])
	}
}

// ─── MergeMaps ────────────────────────────────────────────────────────────────

// 无冲突：两个 map 的 key 各不相同 → 合并所有 key
func TestMergeMaps_NoConflict(t *testing.T) {
	bm := newMerger()
	m1 := map[string]interface{}{"a": 1, "b": 2}
	m2 := map[string]interface{}{"c": 3, "d": 4}

	result, err := bm.MergeMaps(m1, m2, "ctx")
	if err != nil {
		t.Fatalf("MergeMaps 失败: %v", err)
	}
	if len(result) != 4 {
		t.Errorf("期望 4 个 key，得到 %d", len(result))
	}
}

// 有冲突的标量 → auto-resolve=file1 保留 m1 的值
func TestMergeMaps_ScalarConflict_AutoResolveFile1(t *testing.T) {
	bm := newMerger() // auto-resolve = file1
	m1 := map[string]interface{}{"theme": "dark"}
	m2 := map[string]interface{}{"theme": "light"}

	result, err := bm.MergeMaps(m1, m2, "settings")
	if err != nil {
		t.Fatalf("MergeMaps 失败: %v", err)
	}
	if result["theme"] != "dark" {
		t.Errorf("auto-resolve=file1 时期望 theme=dark，得到 %v", result["theme"])
	}
}

// _persist key 应被忽略
func TestMergeMaps_SkipPersistKey(t *testing.T) {
	bm := newMerger()
	m1 := map[string]interface{}{"a": 1}
	m2 := map[string]interface{}{"a": 1, "_persist": "should-be-ignored"}

	result, err := bm.MergeMaps(m1, m2, "ctx")
	if err != nil {
		t.Fatalf("MergeMaps 失败: %v", err)
	}
	if _, exists := result["_persist"]; exists {
		t.Error("_persist 应被过滤，不应出现在合并结果中")
	}
}

// 嵌套数组中的 providers 应按 ID 合并，而不是整体替换
func TestMergeMaps_NestedProvidersArrayMergedByID(t *testing.T) {
	bm := newMerger()
	m1 := map[string]interface{}{
		"providers": []interface{}{
			map[string]interface{}{"id": "openai", "name": "OpenAI"},
		},
	}
	m2 := map[string]interface{}{
		"providers": []interface{}{
			map[string]interface{}{"id": "claude", "name": "Claude"},
		},
	}

	result, err := bm.MergeMaps(m1, m2, "localStorage")
	if err != nil {
		t.Fatalf("MergeMaps 失败: %v", err)
	}

	providers, ok := result["providers"].([]interface{})
	if !ok {
		t.Fatal("providers 应为 []interface{}")
	}
	ids := collectIDs(providers)
	if !reflect.DeepEqual(ids, []string{"claude", "openai"}) {
		t.Errorf("providers 应按 ID 合并，期望 [claude openai]，得到 %v", ids)
	}
}

// ─── MergeData 集成测试 ───────────────────────────────────────────────────────

func TestMergeData_BothProvidersPreserved(t *testing.T) {
	bm := newMerger()

	data1 := &BackupData{
		Time:         1000,
		Version:      5,
		LocalStorage: map[string]interface{}{},
		IndexedDB: map[string]interface{}{
			"providers": []interface{}{
				map[string]interface{}{"id": "openai", "name": "OpenAI"},
			},
		},
	}
	data2 := &BackupData{
		Time:         2000,
		Version:      5,
		LocalStorage: map[string]interface{}{},
		IndexedDB: map[string]interface{}{
			"providers": []interface{}{
				map[string]interface{}{"id": "claude", "name": "Claude"},
			},
		},
	}

	merged, err := bm.MergeData(data1, data2)
	if err != nil {
		t.Fatalf("MergeData 失败: %v", err)
	}

	providers, ok := merged.IndexedDB["providers"].([]interface{})
	if !ok {
		t.Fatal("IndexedDB.providers 应为 []interface{}")
	}

	ids := collectIDs(providers)
	if !reflect.DeepEqual(ids, []string{"claude", "openai"}) {
		t.Errorf("两个提供商都应保留，期望 [claude openai]，得到 %v", ids)
	}

	// 时间戳取较大值
	if merged.Time != 2000 {
		t.Errorf("Time 应取较大值 2000，得到 %d", merged.Time)
	}
}

func TestMergeData_TopicsFromBothFilesPreserved(t *testing.T) {
	bm := newMerger()

	data1 := &BackupData{
		Version:      5,
		LocalStorage: map[string]interface{}{},
		IndexedDB: map[string]interface{}{
			"topics": []interface{}{
				map[string]interface{}{"id": "t1", "title": "对话1"},
			},
		},
	}
	data2 := &BackupData{
		Version:      5,
		LocalStorage: map[string]interface{}{},
		IndexedDB: map[string]interface{}{
			"topics": []interface{}{
				map[string]interface{}{"id": "t2", "title": "对话2"},
			},
		},
	}

	merged, err := bm.MergeData(data1, data2)
	if err != nil {
		t.Fatalf("MergeData 失败: %v", err)
	}

	topics, ok := merged.IndexedDB["topics"].([]interface{})
	if !ok {
		t.Fatal("IndexedDB.topics 应为 []interface{}")
	}
	if len(topics) != 2 {
		t.Errorf("两个对话都应保留，期望 2 条，得到 %d 条", len(topics))
	}
}

func writePersistToLevelDB(t *testing.T, leveldbDir, value string) {
	t.Helper()
	db, err := leveldb.OpenFile(leveldbDir, nil)
	if err != nil {
		t.Fatalf("创建 leveldb 失败: %v", err)
	}
	defer db.Close()
	if err := db.Put([]byte("persist:cherry-studio"), []byte(value), nil); err != nil {
		t.Fatalf("写入 persist:cherry-studio 失败: %v", err)
	}
}

func createDirectInputForTest(t *testing.T, persist string, timestamp int64, marker string) *BackupInput {
	t.Helper()
	root := t.TempDir()
	leveldbDir := filepath.Join(root, "Local Storage", "leveldb")
	if err := os.MkdirAll(leveldbDir, 0755); err != nil {
		t.Fatalf("创建 Local Storage 目录失败: %v", err)
	}
	writePersistToLevelDB(t, leveldbDir, persist)

	indexedDBDir := filepath.Join(root, "IndexedDB")
	if err := os.MkdirAll(indexedDBDir, 0755); err != nil {
		t.Fatalf("创建 IndexedDB 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(indexedDBDir, marker+".txt"), []byte(marker), 0644); err != nil {
		t.Fatalf("写入 IndexedDB 标记文件失败: %v", err)
	}

	dataDir := filepath.Join(root, "Data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("创建 Data 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, marker+".data"), []byte(marker), 0644); err != nil {
		t.Fatalf("写入 Data 标记文件失败: %v", err)
	}

	return &BackupInput{
		Kind:       BackupKindDirect,
		ExtractDir: root,
		Metadata: &DirectMetadata{
			Version:    6,
			Timestamp:  timestamp,
			AppName:    "Cherry Studio",
			AppVersion: "1.0.0",
			Platform:   "darwin",
			Arch:       "arm64",
		},
	}
}

func createDirectZipForTest(t *testing.T, zipPath string, metadata DirectMetadata) {
	t.Helper()
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("创建zip失败: %v", err)
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	defer zw.Close()

	metaWriter, err := zw.Create("metadata.json")
	if err != nil {
		t.Fatalf("创建 metadata.json 失败: %v", err)
	}
	if err := json.NewEncoder(metaWriter).Encode(metadata); err != nil {
		t.Fatalf("写 metadata.json 失败: %v", err)
	}

	idxWriter, err := zw.Create("IndexedDB/dummy.txt")
	if err != nil {
		t.Fatalf("创建 IndexedDB 内容失败: %v", err)
	}
	if _, err := idxWriter.Write([]byte("dummy")); err != nil {
		t.Fatalf("写 IndexedDB 内容失败: %v", err)
	}

	lsWriter, err := zw.Create("Local Storage/leveldb/dummy.txt")
	if err != nil {
		t.Fatalf("创建 Local Storage 内容失败: %v", err)
	}
	if _, err := lsWriter.Write([]byte("dummy")); err != nil {
		t.Fatalf("写 Local Storage 内容失败: %v", err)
	}
}

func TestExtractFromZip_DetectDirectFormat(t *testing.T) {
	bm := newMerger()
	zipPath := filepath.Join(t.TempDir(), "direct.zip")
	createDirectZipForTest(t, zipPath, DirectMetadata{Version: 6, Timestamp: 1000, AppName: "Cherry Studio"})

	input, err := bm.ExtractFromZip(zipPath, filepath.Join(t.TempDir(), "extract"))
	if err != nil {
		t.Fatalf("ExtractFromZip 失败: %v", err)
	}
	if input.Kind != BackupKindDirect {
		t.Fatalf("期望识别为 direct，得到 %s", input.Kind)
	}
	if input.Metadata == nil || input.Metadata.Version != 6 {
		t.Fatalf("metadata 解析异常: %+v", input.Metadata)
	}
}

func TestMergeDirectPractical_UsesPreferredDirectSourceAndMergesPersist(t *testing.T) {
	bm := NewBackupMerger("file2")
	input1 := createDirectInputForTest(t, `{"settings":{"theme":"dark"}}`, 1000, "from1")
	input2 := createDirectInputForTest(t, `{"settings":{"theme":"light"},"extra":1}`, 2000, "from2")

	result, err := bm.MergeDirectPractical(input1, input2)
	if err != nil {
		t.Fatalf("MergeDirectPractical 失败: %v", err)
	}

	expectedIndexedDB := filepath.Join(input2.ExtractDir, "IndexedDB")
	if result.IndexedDBSourceDir != expectedIndexedDB {
		t.Fatalf("IndexedDB 来源选择错误，期望 %s，得到 %s", expectedIndexedDB, result.IndexedDBSourceDir)
	}

	merged := map[string]interface{}{}
	if err := json.Unmarshal([]byte(result.MergedPersist), &merged); err != nil {
		t.Fatalf("MergedPersist 不是合法JSON: %v", err)
	}
	settings, ok := merged["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("MergedPersist 缺少 settings: %v", merged)
	}
	if settings["theme"] != "light" {
		t.Fatalf("file2 策略下 theme 应为 light，得到 %v", settings["theme"])
	}
}

func TestMergeDirectPractical_LegacyAndDirect(t *testing.T) {
	bm := NewBackupMerger("file1")
	legacy := &BackupInput{
		Kind: BackupKindLegacy,
		LegacyData: &BackupData{
			Time:    500,
			Version: 5,
			LocalStorage: map[string]interface{}{
				"persist:cherry-studio": `{"legacy":true}`,
			},
			IndexedDB: map[string]interface{}{},
		},
	}
	direct := createDirectInputForTest(t, `{"direct":true}`, 1000, "direct")

	result, err := bm.MergeDirectPractical(legacy, direct)
	if err != nil {
		t.Fatalf("legacy+direct 合并失败: %v", err)
	}
	if !strings.Contains(result.LocalStorageSourceDir, direct.ExtractDir) {
		t.Fatalf("混合输入时应使用 direct 的目录，得到 %s", result.LocalStorageSourceDir)
	}

	merged := map[string]interface{}{}
	if err := json.Unmarshal([]byte(result.MergedPersist), &merged); err != nil {
		t.Fatalf("MergedPersist 非法: %v", err)
	}
	if merged["legacy"] != true || merged["direct"] != true {
		t.Fatalf("MergedPersist 应同时包含 legacy/direct: %v", merged)
	}
}

func TestSaveDirectBackup_WritesMetadataAndPersist(t *testing.T) {
	bm := NewBackupMerger("file1")
	input := createDirectInputForTest(t, `{"a":1}`, 1234, "base")
	result := &DirectMergeResult{
		Metadata:              DirectMetadata{Version: 6, Timestamp: 9999, AppName: "Cherry Studio"},
		MergedPersist:         `{"merged":true}`,
		LocalStorageSourceDir: filepath.Join(input.ExtractDir, "Local Storage"),
		IndexedDBSourceDir:    filepath.Join(input.ExtractDir, "IndexedDB"),
		DataSourceDirs:        []string{filepath.Join(input.ExtractDir, "Data")},
	}

	outputZip := filepath.Join(t.TempDir(), "merged-direct.zip")
	if err := bm.SaveDirectBackup(result, outputZip); err != nil {
		t.Fatalf("SaveDirectBackup 失败: %v", err)
	}

	reader, err := zip.OpenReader(outputZip)
	if err != nil {
		t.Fatalf("打开输出zip失败: %v", err)
	}
	defer reader.Close()

	hasMetadata := false
	for _, f := range reader.File {
		if f.Name == "metadata.json" {
			hasMetadata = true
			break
		}
	}
	if !hasMetadata {
		t.Fatal("输出zip缺少 metadata.json")
	}

	extractDir := filepath.Join(t.TempDir(), "extract-output")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		t.Fatalf("创建解压目录失败: %v", err)
	}
	for _, f := range reader.File {
		path := filepath.Join(extractDir, f.Name)
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(path, 0755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("创建父目录失败: %v", err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开zip条目失败: %v", err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("读取zip条目失败: %v", err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatalf("写解压文件失败: %v", err)
		}
	}

	persist, err := ReadPersistCherryStudio(filepath.Join(extractDir, "Local Storage", "leveldb"))
	if err != nil {
		t.Fatalf("读取输出 persist 失败: %v", err)
	}
	if persist != `{"merged":true}` {
		t.Fatalf("persist 回写不正确，期望 {\"merged\":true}，得到 %s", persist)
	}
}

// ─── Chromium LevelDB key tests ───────────────────────────────────────────────

func writePersistChromiumKey(t *testing.T, leveldbDir, value string) {
	t.Helper()
	db, err := leveldb.OpenFile(leveldbDir, nil)
	if err != nil {
		t.Fatalf("创建 leveldb 失败: %v", err)
	}
	defer db.Close()
	// Write with Chromium format: UTF-16LE with 0x00 prefix
	encoded := append([]byte{0x00}, EncodeUTF16LE(value)...)
	if err := db.Put(GetChromiumPersistKey(), encoded, nil); err != nil {
		t.Fatalf("写入 chromium key 失败: %v", err)
	}
}

func TestReadPersistCherryStudio_ChromiumKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leveldb")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writePersistChromiumKey(t, dir, `{"theme":"dark"}`)

	result, err := ReadPersistCherryStudio(dir)
	if err != nil {
		t.Fatalf("ReadPersistCherryStudio 失败: %v", err)
	}
	if result != `{"theme":"dark"}` {
		t.Fatalf("期望 {\"theme\":\"dark\"}，得到 %s", result)
	}
}

func TestReadPersistCherryStudio_FallbackToSimpleKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leveldb")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writePersistToLevelDB(t, dir, `{"simple":true}`)

	result, err := ReadPersistCherryStudio(dir)
	if err != nil {
		t.Fatalf("ReadPersistCherryStudio 失败: %v", err)
	}
	if result != `{"simple":true}` {
		t.Fatalf("期望 {\"simple\":true}，得到 %s", result)
	}
}

func TestWritePersistCherryStudio_UpdatesChromiumKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leveldb")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// First write a chromium key
	writePersistChromiumKey(t, dir, `{"old":true}`)

	// Now write via WritePersistCherryStudio - should update chromium key
	if err := WritePersistCherryStudio(dir, `{"new":true}`); err != nil {
		t.Fatalf("WritePersistCherryStudio 失败: %v", err)
	}

	// Read back - should get new value
	result, err := ReadPersistCherryStudio(dir)
	if err != nil {
		t.Fatalf("ReadPersistCherryStudio 失败: %v", err)
	}
	if result != `{"new":true}` {
		t.Fatalf("期望 {\"new\":true}，得到 %s", result)
	}
}

func TestDecodeUTF16LE_BasicASCII(t *testing.T) {
	input := []byte{'h', 0, 'i', 0}
	result := DecodeUTF16LE(input)
	if result != "hi" {
		t.Fatalf("期望 'hi'，得到 '%s'", result)
	}
}

func TestDecodeUTF16LE_Chinese(t *testing.T) {
	// 测 = U+6D4B → bytes 0x4B, 0x6D
	input := EncodeUTF16LE("测试")
	result := DecodeUTF16LE(input)
	if result != "测试" {
		t.Fatalf("期望 '测试'，得到 '%s'", result)
	}
}

// ─── Data directory merge tests ──────────────────────────────────────────────

func TestMergeDirectPractical_MergesDataDirs(t *testing.T) {
	bm := NewBackupMerger("file1")
	input1 := createDirectInputForTest(t, `{"a":1}`, 2000, "from1")
	input2 := createDirectInputForTest(t, `{"b":2}`, 1000, "from2")

	result, err := bm.MergeDirectPractical(input1, input2)
	if err != nil {
		t.Fatalf("MergeDirectPractical 失败: %v", err)
	}

	if len(result.DataSourceDirs) != 2 {
		t.Fatalf("期望 2 个 Data 目录，得到 %d", len(result.DataSourceDirs))
	}
}

func TestSaveDirectBackup_MergesMultipleDataDirs(t *testing.T) {
	bm := NewBackupMerger("file1")
	input := createDirectInputForTest(t, `{"a":1}`, 1234, "base")

	// Create a second Data directory with additional files
	extraDataDir := filepath.Join(t.TempDir(), "extra-data", "Data")
	if err := os.MkdirAll(extraDataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extraDataDir, "extra.data"), []byte("extra"), 0644); err != nil {
		t.Fatal(err)
	}

	result := &DirectMergeResult{
		Metadata:              DirectMetadata{Version: 6, Timestamp: 9999, AppName: "Cherry Studio"},
		MergedPersist:         `{"merged":true}`,
		LocalStorageSourceDir: filepath.Join(input.ExtractDir, "Local Storage"),
		IndexedDBSourceDir:    filepath.Join(input.ExtractDir, "IndexedDB"),
		DataSourceDirs:        []string{filepath.Join(input.ExtractDir, "Data"), extraDataDir},
	}

	outputZip := filepath.Join(t.TempDir(), "merged.zip")
	if err := bm.SaveDirectBackup(result, outputZip); err != nil {
		t.Fatalf("SaveDirectBackup 失败: %v", err)
	}

	// Verify both files exist in output
	reader, err := zip.OpenReader(outputZip)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	files := map[string]bool{}
	for _, f := range reader.File {
		files[f.Name] = true
	}
	if !files["Data/base.data"] {
		t.Error("输出缺少 Data/base.data (来自第一个 Data 目录)")
	}
	if !files["Data/extra.data"] {
		t.Error("输出缺少 Data/extra.data (来自第二个 Data 目录)")
	}
}

// ─── Legacy data.json preservation test ──────────────────────────────────────

func TestMergeDirectPractical_LegacyDataJSONPreserved(t *testing.T) {
	bm := NewBackupMerger("file1")

	// Create a legacy input with an actual data.json file
	legacyDir := t.TempDir()
	legacyDataDir := filepath.Join(legacyDir, "Data")
	if err := os.MkdirAll(legacyDataDir, 0755); err != nil {
		t.Fatal(err)
	}
	dataJSON := `{"time":500,"version":5,"localStorage":{"persist:cherry-studio":"{\"legacy\":true}"},"indexedDB":{"topics":[{"id":"t1","title":"hello"}]}}`
	if err := os.WriteFile(filepath.Join(legacyDir, "data.json"), []byte(dataJSON), 0644); err != nil {
		t.Fatal(err)
	}

	legacy := &BackupInput{
		Kind: BackupKindLegacy,
		LegacyData: &BackupData{
			Time:    500,
			Version: 5,
			LocalStorage: map[string]interface{}{
				"persist:cherry-studio": `{"legacy":true}`,
			},
			IndexedDB: map[string]interface{}{
				"topics": []interface{}{
					map[string]interface{}{"id": "t1", "title": "hello"},
				},
			},
		},
		ExtractDir: legacyDir,
	}
	direct := createDirectInputForTest(t, `{"direct":true}`, 1000, "direct")

	result, err := bm.MergeDirectPractical(legacy, direct)
	if err != nil {
		t.Fatalf("legacy+direct 合并失败: %v", err)
	}

	if result.LegacyDataJSON == "" {
		t.Fatal("LegacyDataJSON 应不为空")
	}

	// Save and verify legacy-data.json is in output
	outputZip := filepath.Join(t.TempDir(), "merged.zip")
	if err := bm.SaveDirectBackup(result, outputZip); err != nil {
		t.Fatalf("SaveDirectBackup 失败: %v", err)
	}

	reader, err := zip.OpenReader(outputZip)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	found := false
	for _, f := range reader.File {
		if f.Name == "legacy-data.json" {
			found = true
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.Contains(string(data), "topics") {
				t.Error("legacy-data.json 应包含 topics 数据")
			}
			break
		}
	}
	if !found {
		t.Fatal("输出 ZIP 缺少 legacy-data.json")
	}
}
