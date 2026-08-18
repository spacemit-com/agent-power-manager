package pluginresource

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// validatePhysicalTree 拒绝 symlink 和非目录根，确保后续读取不会逃逸 artifact 边界。
func validatePhysicalTree(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("lstat plugin artifact root %q: %w", root, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin artifact root %q is not a physical directory: %w", root, ErrInvalidArtifact)
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return validatePhysicalEntry(path, entry)
	}); err != nil {
		if errors.Is(err, ErrInvalidArtifact) {
			return err
		}
		return fmt.Errorf("walk plugin artifact root %q: %w", root, err)
	}
	return nil
}

// validatePhysicalAncestors 校验可信根以下的既存路径组件，拒绝经祖先 symlink 逃逸。
// 配置根本身由 lifecycle 全局锁协作管理；可移植 stdlib 边界不承诺阻止非协作进程的替换竞态。
func validatePhysicalAncestors(trustedRoot, target string) error {
	relative, err := filepath.Rel(trustedRoot, target)
	if err != nil {
		return fmt.Errorf("resolve plugin artifact path %q below trusted root %q: %w", target, trustedRoot, err)
	}
	if relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("plugin artifact path %q escapes trusted root %q: %w", target, trustedRoot, ErrInvalidArtifact)
	}
	current := trustedRoot
	for _, component := range splitPath(relative) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lstat plugin artifact ancestor %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("plugin artifact ancestor %q is not a physical directory: %w", current, ErrInvalidArtifact)
		}
	}
	return nil
}

func splitPath(path string) []string {
	components := make([]string, 0, 4)
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component != "" && component != "." {
			components = append(components, component)
		}
	}
	return components
}

func validatePhysicalEntry(path string, entry fs.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("lstat plugin artifact path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plugin artifact path %q is a symlink: %w", path, ErrInvalidArtifact)
	}
	return nil
}
