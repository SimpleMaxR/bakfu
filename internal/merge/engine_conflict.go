package merge

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// mergeTable 合并表数据（委托给 mergeArrays）
func (bm *BackupMerger) mergeTable(table1, table2 []interface{}, tableName string) ([]interface{}, error) {
	return bm.MergeArrays(table1, table2, tableName)
}

// MergeArrays 合并对象数组（按 ID 合并，同 ID 不同内容两个都保留）
func (bm *BackupMerger) MergeArrays(arr1, arr2 []interface{}, context string) ([]interface{}, error) {
	merged := make([]interface{}, 0)
	processedIds := make(map[string]bool)

	map1 := make(map[string]interface{})
	map2 := make(map[string]interface{})

	for i, item := range arr1 {
		if itemMap, ok := item.(map[string]interface{}); ok {
			id := GetItemID(itemMap, i)
			map1[id] = item
		}
	}
	for i, item := range arr2 {
		if itemMap, ok := item.(map[string]interface{}); ok {
			id := GetItemID(itemMap, i)
			map2[id] = item
		}
	}

	for id, item1 := range map1 {
		processedIds[id] = true
		merged = append(merged, item1)
		if item2, exists := map2[id]; exists {
			if !reflect.DeepEqual(item1, item2) {
				// Same ID, different content: keep both, rename the second copy
				dup := DuplicateWithNewID(item2, "(文件2)")
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

// DuplicateWithNewID clones a map item, assigns a new ID, and appends a suffix to name/title fields.
func DuplicateWithNewID(item interface{}, suffix string) interface{} {
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
		dup["id"] = origID + "_" + GenerateShortID()
	}

	// Append suffix to display name fields
	for _, field := range []string{"name", "title"} {
		if name, ok := dup[field].(string); ok && name != "" {
			dup[field] = name + " " + suffix
		}
	}

	return dup
}

// GenerateShortID returns a short random-ish ID based on current time.
func GenerateShortID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFF)
}

// MergeValue 合并单个值：智能分发数组/对象/标量
func (bm *BackupMerger) MergeValue(value1, value2 interface{}, context string) (interface{}, error) {
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
			merged, err := bm.MergeValue(parsed1, parsed2, context)
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
		if HasIDField(arr1) || HasIDField(arr2) {
			return bm.MergeArrays(arr1, arr2, context)
		}
		// 无 ID 的普通数组：整体冲突
		return bm.ResolveConflict(Conflict{
			Type:    GetConflictTypeFromContext(context),
			Key:     context,
			Value1:  value1,
			Value2:  value2,
			Context: context,
		})
	}

	map1, isMap1 := value1.(map[string]interface{})
	map2, isMap2 := value2.(map[string]interface{})
	if isMap1 && isMap2 {
		return bm.MergeMaps(map1, map2, context)
	}

	// 标量冲突
	return bm.ResolveConflict(Conflict{
		Type:    GetConflictTypeFromContext(context),
		Key:     context,
		Value1:  value1,
		Value2:  value2,
		Context: context,
	})
}

// MergeMaps 合并 map 数据（按 key 逐一智能合并）
func (bm *BackupMerger) MergeMaps(map1, map2 map[string]interface{}, context string) (map[string]interface{}, error) {
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
				resolved, err := bm.MergeValue(value1, value2, childCtx)
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

// ResolveConflict 解决冲突
func (bm *BackupMerger) ResolveConflict(conflict Conflict) (interface{}, error) {
	bm.Conflicts = append(bm.Conflicts, conflict)

	// 自动解决策略
	if bm.AutoResolve != "" {
		return bm.AutoResolveConflict(conflict), nil
	}

	// 交互式解决
	return bm.InteractiveResolve(conflict)
}

// AutoResolveConflict 自动解决冲突
func (bm *BackupMerger) AutoResolveConflict(conflict Conflict) interface{} {
	switch bm.AutoResolve {
	case "newer":
		time1 := GetTimestamp(conflict.Value1)
		time2 := GetTimestamp(conflict.Value2)
		if time2 > time1 {
			return conflict.Value2
		}
		return conflict.Value1
	case "older":
		time1 := GetTimestamp(conflict.Value1)
		time2 := GetTimestamp(conflict.Value2)
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

// InteractiveResolve 交互式解决冲突
func (bm *BackupMerger) InteractiveResolve(conflict Conflict) (interface{}, error) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("⚠️  发现冲突: %s\n", conflict.Type)
	fmt.Printf("📍 位置: %s\n", conflict.Context)
	fmt.Println(strings.Repeat("=", 80))

	fmt.Println("\n📄 选项 1 (来自第一个文件):")
	bm.PrintValue(conflict.Value1)

	fmt.Println("\n📄 选项 2 (来自第二个文件):")
	bm.PrintValue(conflict.Value2)

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
			bm.ShowDiff(conflict.Value1, conflict.Value2)
			continue
		case "s":
			return conflict.Value1, nil
		default:
			fmt.Println("❌ 无效选择，请输入 1、2、d 或 s")
			continue
		}
	}
}
