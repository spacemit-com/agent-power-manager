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
	snapshot, err := c.validateSkillSource(ctx, source)
	if err != nil {
		return err
	}
	destination, err = absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateTreeDestination(destination); err != nil {
		return err
	}
	if err := validateCopyBoundary(source, destination); err != nil {
		return err
	}
	return copyTreeAtomically(ctx, snapshot, destination, "Skill", c.beforeTreePublish)
}

func (c *Client) validateSkillSource(ctx context.Context, source string) (*treeSnapshot, error) {
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, cloud.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("stat source Skill %q: %w", source, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, unsafeContent("source Skill %q is not a physical directory", source)
	}
	for _, markerName := range []string{"SKILL.md", "skill-card.md"} {
		markerPath := filepath.Join(source, markerName)
		marker, markerErr := os.Lstat(markerPath)
		if errors.Is(markerErr, fs.ErrNotExist) {
			return nil, unsafeContent("source Skill marker %q is missing", markerPath)
		}
		if markerErr != nil {
			return nil, fmt.Errorf("stat source Skill marker %q: %w", markerPath, markerErr)
		}
		if !marker.Mode().IsRegular() || marker.Mode()&os.ModeSymlink != 0 {
			return nil, unsafeContent("source Skill marker %q is not a physical regular file", markerPath)
		}
	}
	snapshot, err := capturePhysicalTree(ctx, c.resourcesRoot, source, c.beforeTreeSnapshot, c.beforeSnapshotFileOpen, c.afterSnapshotDataOpen)
	if err != nil {
		return nil, err
	}
	if err := snapshot.requireRegularFile("SKILL.md"); err != nil {
		return nil, err
	}
	if err := snapshot.requireRegularFile("skill-card.md"); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func isCanonicalVersion(version string) bool {
	_, valid := canonicalPowerVersion(version)
	return valid
}
