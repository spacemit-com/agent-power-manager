package remote

import (
	"context"
	"fmt"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var _ cloud.PluginSource = (*Client)(nil)

// DownloadPlugin 验证精确版本 Plugin 归档与物理根 marker；apc-plugin.json 的领域语义由 Task 7 处理。
func (c *Client) DownloadPlugin(ctx context.Context, entry domain.CatalogEntry, destination string) error {
	ref := entry.Ref
	if ref.Type != domain.PowerTypePlugin || !validResolveQuery(ref) || ref.Version == "" {
		return fmt.Errorf("download invalid remote Plugin identity: %w", cloud.ErrNotFound)
	}
	return c.downloadArchivePower(ctx, entry, destination, "apc-plugin.json")
}
