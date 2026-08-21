package cloud

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/utils/filesystem"
)

// DownloadSkill 将精确版本 Skill 原子复制至调用方提供的私有目录，不访问全局缓存。
func (c *LocalClient) DownloadSkill(ctx context.Context, ref domain.PowerRef, destination string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Type != domain.PowerTypeSkill || !safePowerIdentity(ref) || !isCanonicalVersion(ref.Version) {
		return ErrNotFound
	}
	source := filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version)
	if err := validateSkillSource(source); err != nil {
		return err
	}
	destination, err := absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateEmptyPhysicalDirectory(destination); err != nil {
		return err
	}
	if err := validateSkillCopyBoundary(source, destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create Skill destination parent %q: %w", filepath.Dir(destination), err)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return fmt.Errorf("create Skill temporary destination beside %q: %w", destination, err)
	}
	defer func() {
		if err := os.RemoveAll(temporary); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove Skill temporary destination %q: %w", temporary, err))
		}
	}()
	if err := filesystem.CopyDir(source, temporary); err != nil {
		return fmt.Errorf("copy source Skill %q: %w", source, err)
	}
	if err := validateEmptyPhysicalDirectory(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Remove(destination); err != nil {
			return fmt.Errorf("remove empty Skill destination %q: %w", destination, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("recheck Skill destination %q: %w", destination, err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish Skill destination %q: %w", destination, err)
	}
	return nil
}

func validateSkillSource(source string) error {
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0)) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source Skill %q: %w", source, err)
	}
	marker, err := os.Lstat(filepath.Join(source, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !marker.Mode().IsRegular()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source Skill marker %q: %w", source, err)
	}
	return nil
}

func isCanonicalVersion(version string) bool {
	_, valid := canonicalPowerVersion(version)
	return valid
}

func absoluteDestination(destination string) (string, error) {
	if destination == "" {
		return "", errors.New("destination is empty")
	}
	resolved, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolve destination %q: %w", destination, err)
	}
	return filepath.Clean(resolved), nil
}

func validateEmptyPhysicalDirectory(destination string) error {
	if err := validatePluginDestinationAncestors(destination); err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat destination %q: %w", destination, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("destination %q is not a physical directory", destination)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("read destination %q: %w", destination, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination %q is not empty", destination)
	}
	return nil
}

func validateSkillCopyBoundary(source, destination string) error {
	physicalSource, err := filesystem.ResolvePhysicalPath(source)
	if err != nil {
		return fmt.Errorf("resolve physical source Skill %q: %w", source, err)
	}
	physicalDestination, err := filesystem.ResolvePhysicalPath(destination)
	if err != nil {
		return fmt.Errorf("resolve physical Skill destination %q: %w", destination, err)
	}
	inside, err := filesystem.PathEqualOrWithin(physicalSource, physicalDestination)
	if err != nil {
		return fmt.Errorf("compare source Skill %q and destination %q: %w", source, destination, err)
	}
	if inside {
		return fmt.Errorf("Skill destination %q must not equal or be contained by source %q", destination, source)
	}
	return nil
}
