package merge

import (
	"encoding/json"
	"fmt"
)

// MergeData 合并两个备份数据
func (bm *BackupMerger) MergeData(data1, data2 *BackupData) (*BackupData, error) {
	fmt.Println("\n🔄 开始合并数据...")

	merged := &BackupData{
		Time:         MaxInt64(data1.Time, data2.Time),
		Version:      MaxInt(data1.Version, data2.Version),
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

// mergeLocalStorage 合并LocalStorage
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

	mergedPersist, err := bm.MergeMaps(persist1, persist2, "localStorage.persist:cherry-studio")
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

// mergeIndexedDB 合并IndexedDB
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

// SaveBackup 保存备份数据
func (bm *BackupMerger) SaveBackup(data *BackupData, outputPath, format string) error {
	fmt.Printf("💾 保存合并结果: %s\n", outputPath)

	// 创建输出目录
	if err := MkdirAll(outputPath); err != nil {
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
		return bm.saveByExtension(data, outputPath)
	}
}
