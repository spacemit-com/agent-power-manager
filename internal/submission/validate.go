package submission

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
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

type sourceTreeSnapshot struct {
	entries []archiveEntry
	files   map[string][]byte
	stats   sourceStats
}

func (snapshot sourceTreeSnapshot) regularFile(relative string) ([]byte, error) {
	canonical := path.Clean(filepath.ToSlash(relative))
	contents, ok := snapshot.files[canonical]
	if !ok {
		return nil, fmt.Errorf("required file %q is missing: %w", relative, ErrInvalid)
	}
	return append([]byte(nil), contents...), nil
}

func (snapshot sourceTreeSnapshot) build() ([]byte, error) {
	return buildTarGz(snapshot.entries)
}

// validateSourceTree 保留目录安全校验入口；Loader 使用 captureSourceTree 同时保留不可变快照。
func validateSourceTree(root string) (sourceStats, error) {
	snapshot, err := captureSourceTree(context.Background(), root)
	if err != nil {
		return sourceStats{}, err
	}
	return snapshot.stats, nil
}

// captureSourceTree 在同一次物理校验读取中固定路径、mode 与正文；后续打包不得重读源目录。
func captureSourceTree(ctx context.Context, root string) (sourceTreeSnapshot, error) {
	if err := pluginartifact.ValidatePhysicalTree(root); err != nil {
		return sourceTreeSnapshot{}, fmt.Errorf("validate source tree: %w", errors.Join(ErrInvalid, err))
	}
	snapshot := sourceTreeSnapshot{files: make(map[string][]byte)}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
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
			snapshot.entries = append(snapshot.entries, archiveEntry{name: slashed + "/", mode: 0o755, typeflag: tar.TypeDir})
			return nil
		}
		snapshot.stats.fileCount++
		if snapshot.stats.fileCount > maxFileCount {
			return fmt.Errorf("file count exceeds %d: %w", maxFileCount, ErrInvalid)
		}
		contents, mode, err := readPhysicalRegularFile(ctx, path, maxSingleFileSize)
		if err != nil {
			return err
		}
		snapshot.stats.totalSize += int64(len(contents))
		if snapshot.stats.totalSize > maxSourceTotalSize {
			return fmt.Errorf("source total size exceeds %d bytes: %w", maxSourceTotalSize, ErrInvalid)
		}
		if contentpolicy.IsUnsafeText(contents) {
			return fmt.Errorf("file %q contains binary or archive content: %w", slashed, ErrInvalid)
		}
		body := append([]byte(nil), contents...)
		snapshot.files[slashed] = body
		snapshot.entries = append(snapshot.entries, archiveEntry{name: slashed, mode: archiveFileMode(mode), typeflag: tar.TypeReg, body: body})
		return nil
	})
	if err != nil {
		return sourceTreeSnapshot{}, err
	}
	return snapshot, nil
}
