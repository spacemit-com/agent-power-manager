package catalog

import (
	"fmt"
	"path/filepath"

	"apm/internal/domain"
)

// Repository 在物理文件安全边界内加载和原子替换唯一的 catalog 快照。
type Repository interface {
	Load() (Snapshot, domain.CatalogVersion, error)
	Replace(expected domain.CatalogVersion, sourcePath string) (Snapshot, error)
}

type repositoryPlatform interface {
	load(path string) (Snapshot, domain.CatalogVersion, error)
	replace(path string, expected domain.CatalogVersion, sourcePath string) (Snapshot, error)
}

type filesystemRepository struct {
	path     string
	platform repositoryPlatform
}

// NewRepository 创建固定到绝对、规范文件路径的 Repository；构造过程不访问文件系统。
func NewRepository(path string) (Repository, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("catalog repository path %q must be absolute and clean", path)
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return nil, fmt.Errorf("catalog repository path %q must name a file", path)
	}
	return &filesystemRepository{path: path, platform: newRepositoryPlatform()}, nil
}

// Load 从同一物理普通文件的受限字节中计算版本并解码不可变快照。
func (repository *filesystemRepository) Load() (Snapshot, domain.CatalogVersion, error) {
	return repository.platform.load(repository.path)
}

// Replace 验证 provider staged 文件与期望版本，再以 0600 模式原子替换 catalog。
func (repository *filesystemRepository) Replace(expected domain.CatalogVersion, sourcePath string) (Snapshot, error) {
	if err := expected.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("validate expected catalog version: %w", err)
	}
	if expected.Size > MaxCatalogSize {
		return Snapshot{}, fmt.Errorf("expected catalog size %d exceeds %d bytes: %w", expected.Size, MaxCatalogSize, ErrInvalidCatalog)
	}
	if sourcePath == "" || !filepath.IsAbs(sourcePath) || filepath.Clean(sourcePath) != sourcePath {
		return Snapshot{}, fmt.Errorf("catalog staged source path %q must be absolute and clean", sourcePath)
	}
	if sourcePath == repository.path {
		return Snapshot{}, fmt.Errorf("catalog staged source must differ from repository path %q", repository.path)
	}
	return repository.platform.replace(repository.path, expected, sourcePath)
}

var _ Repository = (*filesystemRepository)(nil)
