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
func (c *Client) DownloadSkill(ctx context.Context, entry domain.CatalogEntry, destination string) error {
	ref := entry.Ref
	if ref.Type != domain.PowerTypeSkill || !validResolveQuery(ref) || ref.Version == "" {
		return fmt.Errorf("download invalid remote Skill identity: %w", cloud.ErrNotFound)
	}
	return c.downloadArchivePower(ctx, entry, destination, nil, "SKILL.md", "skill-card.md")
}

// downloadArchivePower 是 Skill/Plugin/Agent 共享的目录下载流：验证并下载 gzip 归档，
// 按调用方策略解包到私有输出并发布；policy 为 nil 时保持文本-only 默认行为。
func (c *Client) downloadArchivePower(ctx context.Context, entry domain.CatalogEntry, destination string, policy *archivePolicy, requiredMarkers ...string) (resultErr error) {
	ref := entry.Ref
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
	archive, err := c.downloadVerifiedContent(ctx, entry, "application/gzip", prepared.parent, 0o600)
	if err != nil {
		return fmt.Errorf("download remote %s archive content: %w", ref.Type, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, archive.remove())
	}()
	if err := c.extractArchiveIntoWithPolicy(ctx, archive, prepared, archiveHooks{}, policy, requiredMarkers...); err != nil {
		return fmt.Errorf("extract remote %s archive: %w", ref.Type, err)
	}
	return nil
}
