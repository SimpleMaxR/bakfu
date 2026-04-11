package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bakfu/internal/merge"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

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
		fmt.Printf("Bakfu %s (built %s)\n", Version, BuildTime)
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
	merger := merge.NewBackupMerger(*autoResolve)
	tempDir := "temp-merge"

	// 清理函数
	defer func() {
		os.RemoveAll(tempDir)
	}()

	// 加载数据
	var input1, input2 *merge.BackupInput
	var err error

	// 加载第一个文件
	if strings.HasSuffix(strings.ToLower(file1), ".zip") {
		extractDir1 := filepath.Join(tempDir, "extract1")
		input1, err = merger.ExtractFromZip(file1, extractDir1)
	} else {
		data, loadErr := merger.LoadFromJSON(file1)
		err = loadErr
		if loadErr == nil {
			input1 = &merge.BackupInput{Kind: merge.BackupKindLegacy, LegacyData: data}
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
			input2 = &merge.BackupInput{Kind: merge.BackupKindLegacy, LegacyData: data}
		}
	}

	if err != nil {
		log.Fatalf("❌ 加载文件2失败: %v", err)
	}

	printInputInfo := func(label string, input *merge.BackupInput) {
		fmt.Printf("\n📋 %s信息:\n", label)
		switch input.Kind {
		case merge.BackupKindLegacy:
			if input.LegacyData != nil {
				fmt.Printf("  类型: legacy(data.json)\n")
				fmt.Printf("  时间: %s\n", time.Unix(input.LegacyData.Time/1000, 0).Format("2006-01-02 15:04:05"))
				fmt.Printf("  版本: %d\n", input.LegacyData.Version)
			}
		case merge.BackupKindDirect:
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

	if input1.Kind == merge.BackupKindLegacy && input2.Kind == merge.BackupKindLegacy {
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
