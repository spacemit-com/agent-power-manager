package remote

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var _ cloud.AgentSource = (*Client)(nil)

// DownloadAgent 验证精确版本 Agent 的原始字节，并通过同目录临时文件原子发布到尚不存在的绝对目标。
func (c *Client) DownloadAgent(ctx context.Context, ref domain.PowerRef, destination string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download remote Agent: %w", err)
	}
	if ref.Type != domain.PowerTypeAgent || !validResolveQuery(ref) || ref.Version == "" {
		return fmt.Errorf("download invalid remote Agent identity: %w", cloud.ErrNotFound)
	}
	parentPath, destinationName, err := splitRemoteAgentDestination(destination)
	if err != nil {
		return err
	}
	parent, err := openPinnedDirectory(parentPath)
	if err != nil {
		return fmt.Errorf("open remote Agent destination parent: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, parent.close())
	}()
	if err := parent.requireAbsent(destinationName); err != nil {
		return err
	}
	temporary, err := c.downloadVerifiedContent(ctx, ref, "application/yaml", parent, 0o644)
	if err != nil {
		return fmt.Errorf("download remote Agent content: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, temporary.remove())
	}()
	if c.beforeAgentPublish != nil {
		c.beforeAgentPublish(temporary)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish remote Agent: %w", err)
	}
	if err := temporary.publishNoReplace(destinationName); err != nil {
		return fmt.Errorf("publish remote Agent destination %q: %w", destination, err)
	}
	return nil
}

func splitRemoteAgentDestination(destination string) (string, string, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return "", "", fmt.Errorf("remote Agent destination %q must be an absolute clean path: %w", destination, cloud.ErrUnsafeContent)
	}
	name := filepath.Base(destination)
	if !validSecureEntryName(name) {
		return "", "", fmt.Errorf("remote Agent destination %q has invalid base name: %w", destination, cloud.ErrUnsafeContent)
	}
	return filepath.Dir(destination), name, nil
}
