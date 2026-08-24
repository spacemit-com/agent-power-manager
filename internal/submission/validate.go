package submission

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/cloud/contentpolicy"
	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

// 源目录阈值与 APC §5.6 对齐；解压相关阈值由 APM 在打包前拦截，
// 上传大小 20 MiB 是 APC 的同步接收边界，在 Prepare 打包后另行校验。
const (
	maxSourceTotalSize = 100 << 20 // 100 MiB
	maxSingleFileSize  = 10 << 20  // 10 MiB
	maxFileCount       = 1000
	maxDirectoryDepth  = 16
	maxPathLength      = 255
)

// Validate 校验 Skill/Plugin/MCP/Agent 源并返回解析出的身份与统计信息。
// 完全离线，不读取 API Key，也不访问网络。
func Validate(ctx context.Context, request Request) (Validation, error) {
	loaded, err := loadSource(ctx, request.Type, request.Path)
	if err != nil {
		return Validation{}, err
	}
	return loaded.validation, nil
}

// validateCommonMetadata 校验三类共用的元数据规则。
func validateCommonMetadata(name, version, description string) error {
	if !domain.IsCanonicalPowerName(name) {
		return fmt.Errorf("name %q is not canonical 1-64 lowercase kebab: %w", name, ErrInvalid)
	}
	if _, err := domain.CanonicalPowerVersion(version); err != nil {
		return fmt.Errorf("version: %w", errors.Join(ErrInvalid, err))
	}
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("description is empty: %w", ErrInvalid)
	}
	return nil
}

type sourceStats struct {
	fileCount int
	totalSize int64
}

// readRootRegularFile 读取资源根下的普通文件，缺失或非常规文件均报错。
func readRootRegularFile(root, name string) ([]byte, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("required file %q is missing: %w", name, ErrInvalid)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("required file %q is not a regular file: %w", name, ErrInvalid)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return contents, nil
}

// validateSourceTree 对资源根目录执行物理树安全、内容安全与阈值校验，
// 返回文件数与总大小（预计内容大小）。
func validateSourceTree(root string) (sourceStats, error) {
	if err := pluginartifact.ValidatePhysicalTree(root); err != nil {
		return sourceStats{}, fmt.Errorf("validate source tree: %w", errors.Join(ErrInvalid, err))
	}
	var stats sourceStats
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		if len(slashed) > maxPathLength {
			return fmt.Errorf("path %q exceeds %d bytes: %w", slashed, maxPathLength, ErrInvalid)
		}
		depth := strings.Count(slashed, "/") + 1
		if depth > maxDirectoryDepth {
			return fmt.Errorf("path %q exceeds max depth %d: %w", slashed, maxDirectoryDepth, ErrInvalid)
		}
		if entry.IsDir() {
			return nil
		}
		// 物理树校验已保证此处为普通文件。
		stats.fileCount++
		if stats.fileCount > maxFileCount {
			return fmt.Errorf("file count exceeds %d: %w", maxFileCount, ErrInvalid)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size := info.Size()
		if size > maxSingleFileSize {
			return fmt.Errorf("file %q exceeds single-file limit %d bytes: %w", slashed, maxSingleFileSize, ErrInvalid)
		}
		stats.totalSize += size
		if stats.totalSize > maxSourceTotalSize {
			return fmt.Errorf("source total size exceeds %d bytes: %w", maxSourceTotalSize, ErrInvalid)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if contentpolicy.IsUnsafeText(contents) {
			return fmt.Errorf("file %q contains binary or archive content: %w", slashed, ErrInvalid)
		}
		return nil
	})
	if err != nil {
		return sourceStats{}, err
	}
	return stats, nil
}
