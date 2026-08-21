package remote

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

var _ cloud.AgentSource = (*Client)(nil)

// DownloadAgent 验证精确版本 Agent 的原始字节，并通过同目录临时文件原子发布到尚不存在的绝对目标。
func (c *Client) DownloadAgent(ctx context.Context, ref domain.PowerRef, destination string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download remote Agent: %w", err)
	}
	if ref.Type != domain.PowerTypeAgent || !validResolveQuery(ref) || ref.Version == "" {
		return fmt.Errorf("download invalid remote Agent identity: %w", cloud.ErrNotFound)
	}
	if err := validateRemoteAgentDestination(destination); err != nil {
		return err
	}
	temporary, err := c.downloadVerifiedContent(ctx, ref, "application/yaml", filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("download remote Agent content: %w", err)
	}
	defer func() {
		if err := os.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove remote Agent temporary file %q: %w", temporary, err))
		}
	}()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("publish remote Agent: %w", err)
	}
	if err := validateRemoteAgentDestination(destination); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish remote Agent destination %q: %w", destination, err)
	}
	if err := os.Chmod(destination, 0o644); err != nil {
		removeErr := os.Remove(destination)
		return errors.Join(
			fmt.Errorf("set remote Agent destination mode %q: %w", destination, err),
			removeErr,
		)
	}
	return nil
}

func validateRemoteAgentDestination(destination string) error {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("remote Agent destination %q must be an absolute clean path: %w", destination, cloud.ErrUnsafeContent)
	}
	if err := validateRemotePhysicalDirectory(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("validate remote Agent destination parent: %w", err)
	}
	if _, err := os.Lstat(destination); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat remote Agent destination %q: %w", destination, errors.Join(cloud.ErrUnsafeContent, err))
	}
	return fmt.Errorf("remote Agent destination %q must not exist: %w", destination, cloud.ErrUnsafeContent)
}
