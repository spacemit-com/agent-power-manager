package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ResolvePhysicalPath 解析已有 symlink，并保留尚不存在的路径后缀。
func ResolvePhysicalPath(path string) (string, error) {
	return resolvePhysicalPathDepth(path, 0)
}

func resolvePhysicalPathDepth(path string, depth int) (string, error) {
	if depth > 255 {
		return "", fmt.Errorf("resolve path %q: too many symbolic links", path)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}

	current := absolute
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", fmt.Errorf("evaluate symlinks for %q: %w", current, err)
		}

		info, lstatErr := os.Lstat(current)
		if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readlinkErr := os.Readlink(current)
			if readlinkErr != nil {
				return "", fmt.Errorf("read symlink %q: %w", current, readlinkErr)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			resolved, resolveErr := resolvePhysicalPathDepth(target, depth+1)
			if resolveErr != nil {
				return "", resolveErr
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) && !errors.Is(lstatErr, syscall.ENOTDIR) {
			return "", fmt.Errorf("inspect path %q: %w", current, lstatErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve existing path prefix for %q: %w", absolute, err)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// PathEqualOrWithin 报告 path 是否等于或位于 parent 目录内。
func PathEqualOrWithin(parent, path string) (bool, error) {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}
