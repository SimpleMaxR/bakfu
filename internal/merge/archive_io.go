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

// ExtractFromZip 从ZIP文件提取备份数据（兼容 legacy 和 direct）
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

// extractFile 解压单个文件
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

// LoadFromJSON 从JSON文件加载备份数据
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
