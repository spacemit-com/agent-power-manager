package remote

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var _ cloud.SkillSource = (*Client)(nil)
var _ cloud.Client = (*Client)(nil)

// DownloadSkill 验证精确版本 Skill 归档及根 marker，并以不覆盖语义发布到调用方的物理空目录。
func (c *Client) DownloadSkill(ctx context.Context, ref domain.PowerRef, destination string) error {
	if ref.Type != domain.PowerTypeSkill || !validResolveQuery(ref) || ref.Version == "" {
		return fmt.Errorf("download invalid remote Skill identity: %w", cloud.ErrNotFound)
	}
	return c.downloadArchivePower(ctx, ref, destination, "SKILL.md", "skill-card.md")
}

func (c *Client) downloadArchivePower(ctx context.Context, ref domain.PowerRef, destination string, requiredMarkers ...string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download remote %s: %w", ref.Type, err)
	}
	prepared, err := prepareArchiveDestination(destination)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, prepared.close())
	}()
	archive, err := c.downloadVerifiedContent(ctx, ref, "application/gzip", prepared.parent, 0o600)
	if err != nil {
		return fmt.Errorf("download remote %s archive content: %w", ref.Type, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, archive.remove())
	}()
	if err := c.extractArchiveInto(ctx, archive, prepared, archiveHooks{}, requiredMarkers...); err != nil {
		return fmt.Errorf("extract remote %s archive: %w", ref.Type, err)
	}
	return nil
}
