package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/cloud"
	"apm/internal/domain"
)

// DownloadSkill 将精确版本 Skill 原子复制至调用方提供的私有目录，不访问全局缓存。
func (c *Client) DownloadSkill(ctx context.Context, ref domain.PowerRef, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.Type != domain.PowerTypeSkill || !safePowerIdentity(ref) || !isCanonicalVersion(ref.Version) {
		return cloud.ErrNotFound
	}
	source := filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version)
	if err := validateSkillSource(ctx, c.resourcesRoot, source); err != nil {
		return err
	}
	destination, err := absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateTreeDestination(destination); err != nil {
		return err
	}
	if err := validateCopyBoundary(source, destination); err != nil {
		return err
	}
	return copyTreeAtomically(ctx, source, destination, "Skill")
}

func validateSkillSource(ctx context.Context, resourcesRoot, source string) error {
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return cloud.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source Skill %q: %w", source, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return unsafeContent("source Skill %q is not a physical directory", source)
	}
	marker, err := os.Lstat(filepath.Join(source, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return cloud.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source Skill marker %q: %w", source, err)
	}
	if !marker.Mode().IsRegular() || marker.Mode()&os.ModeSymlink != 0 {
		return unsafeContent("source Skill marker %q is not a physical regular file", filepath.Join(source, "SKILL.md"))
	}
	return validatePhysicalTree(ctx, resourcesRoot, source)
}

func isCanonicalVersion(version string) bool {
	_, valid := canonicalPowerVersion(version)
	return valid
}
