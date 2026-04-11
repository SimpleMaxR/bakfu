package merge

import (
	"bufio"
	"os"
)

// BackupData 备份数据结构
type BackupData struct {
	Time         int64                  `json:"time"`
	Version      int                    `json:"version"`
	LocalStorage map[string]interface{} `json:"localStorage"`
	IndexedDB    map[string]interface{} `json:"indexedDB"`
}

// BackupKind 备份类型
type BackupKind string

const (
	BackupKindLegacy BackupKind = "legacy"
	BackupKindDirect BackupKind = "direct"
)

// DirectMetadata 新版备份元数据
type DirectMetadata struct {
	Version    int    `json:"version"`
	Timestamp  int64  `json:"timestamp"`
	AppName    string `json:"appName"`
	AppVersion string `json:"appVersion"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
}

// BackupInput 统一的备份输入封装
type BackupInput struct {
	Kind       BackupKind
	LegacyData *BackupData
	ExtractDir string
	Metadata   *DirectMetadata
}

// DirectMergeResult direct 合并结果
type DirectMergeResult struct {
	Metadata              DirectMetadata
	MergedPersist         string
	LocalStorageSourceDir string
	IndexedDBSourceDir    string
	DataSourceDirs        []string // multiple Data dirs to merge (preferred first)
	LegacyDataJSON        string   // path to legacy data.json if one input was legacy (for IndexedDB preservation)
}

// ConflictTypes 冲突类型标签映射
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

// Conflict 冲突信息
type Conflict struct {
	Type     string      `json:"type"`
	Key      string      `json:"key"`
	Value1   interface{} `json:"value1"`
	Value2   interface{} `json:"value2"`
	Context  string      `json:"context"`
	Resolved interface{} `json:"resolved,omitempty"`
}

// BackupMerger 合并器
type BackupMerger struct {
	AutoResolve string
	Conflicts   []Conflict
	Reader      *bufio.Reader
}

// NewBackupMerger 创建新的合并器
func NewBackupMerger(autoResolve string) *BackupMerger {
	return &BackupMerger{
		AutoResolve: autoResolve,
		Conflicts:   make([]Conflict, 0),
		Reader:      bufio.NewReader(os.Stdin),
	}
}
