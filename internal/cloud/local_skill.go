package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/utils/filesystem"
)

func (c *LocalClient) getSkill(ref domain.PowerRef) (resultErr error) {
	if err := os.RemoveAll(c.cacheTempRoot); err != nil {
		return fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err)
	}
	defer func() {
		if err := os.RemoveAll(c.cacheTempRoot); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err))
		}
	}()

	final := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
	temporary := filepath.Join(c.cacheTempRoot, "skills", ref.Name, ref.Version)
	source := filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version)

	if _, err := os.Stat(final); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat cache power %q: %w", final, err)
	}
	sourceInfo, err := os.Stat(source)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !sourceInfo.IsDir()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source skill %q: %w", source, err)
	}
	markerPath := filepath.Join(source, "SKILL.md")
	marker, err := os.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !marker.Mode().IsRegular()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source skill marker %q: %w", markerPath, err)
	}
	if err = filesystem.CopyDir(source, temporary); err != nil {
		return fmt.Errorf("copy source skill %q: %w", source, err)
	}
	if err = os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create cache parent %q: %w", filepath.Dir(final), err)
	}
	if err = os.Rename(temporary, final); err != nil {
		return fmt.Errorf("commit cached skill %q: %w", final, err)
	}
	return nil
}
