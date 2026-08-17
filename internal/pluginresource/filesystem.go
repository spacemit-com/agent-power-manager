package pluginresource

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
