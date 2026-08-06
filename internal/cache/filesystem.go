package cache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/paths"
	"apm/internal/utils/filesystem"
)

// FilesystemCache 从本地版本化缓存读取能力内容。
type FilesystemCache struct {
	cacheRoot       string
	stagingPath     string
	stagingTempPath string
}

// NewFilesystem 创建使用默认缓存和 staging 路径的缓存。
func NewFilesystem() (*FilesystemCache, error) {
	cacheRoot, err := paths.CachePath()
	if err != nil {
		return nil, fmt.Errorf("resolve cache path: %w", err)
	}
	stagingPath, err := paths.StagingPath()
	if err != nil {
		return nil, fmt.Errorf("resolve staging path: %w", err)
	}
	stagingTempPath, err := paths.StagingTempPath()
	if err != nil {
		return nil, fmt.Errorf("resolve staging temporary path: %w", err)
	}
	return &FilesystemCache{
		cacheRoot:       cacheRoot,
		stagingPath:     stagingPath,
		stagingTempPath: stagingTempPath,
	}, nil
}

// Fetch 将指定版本的缓存 Skill 复制到无版本层级的 staging 目录。
func (c *FilesystemCache) Fetch(ref domain.PowerRef) (content Content, resultErr error) {
	if ref.Version == "" {
		return Content{}, errors.New("power version is required")
	}
	if err := os.RemoveAll(c.stagingPath); err != nil {
		return Content{}, fmt.Errorf("remove staging path %q: %w", c.stagingPath, err)
	}
	if err := os.RemoveAll(c.stagingTempPath); err != nil {
		return Content{}, fmt.Errorf("remove staging temporary path %q: %w", c.stagingTempPath, err)
	}
	defer func() {
		if err := os.RemoveAll(c.stagingTempPath); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove staging temporary path %q: %w", c.stagingTempPath, err))
		}
	}()

	if ref.Type != domain.PowerTypeSkill {
		return Content{}, ErrMiss
	}

	source := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Content{}, ErrMiss
		}
		return Content{}, fmt.Errorf("stat cache path %q: %w", source, err)
	}

	target := filepath.Join(c.stagingTempPath, "skills", ref.Name)
	if err := filesystem.CopyDir(source, target); err != nil {
		return Content{}, fmt.Errorf("copy cache path %q to staging path %q: %w", source, target, err)
	}
	if err := os.MkdirAll(filepath.Dir(c.stagingPath), 0o755); err != nil {
		return Content{}, fmt.Errorf("create staging parent path %q: %w", filepath.Dir(c.stagingPath), err)
	}
	if err := os.Rename(c.stagingTempPath, c.stagingPath); err != nil {
		return Content{}, fmt.Errorf("rename staging temporary path %q to staging path %q: %w", c.stagingTempPath, c.stagingPath, err)
	}

	return Content{Path: filepath.Join(c.stagingPath, "skills", ref.Name)}, nil
}
