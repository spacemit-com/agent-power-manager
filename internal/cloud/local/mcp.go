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

var _ cloud.MCPUninstallDefinitionSource = (*Client)(nil)

// GetMCP 从一次 fresh Local catalog content 读取定义；entry metadata 必须与该物理快照精确一致。
func (c *Client) GetMCP(ctx context.Context, entry domain.CatalogEntry) (domain.MCPServer, error) {
	content, err := c.loadMatchingCatalogContent(ctx, entry, domain.PowerTypeMCP)
	if err != nil {
		return domain.MCPServer{}, err
	}
	return c.decodeMCPContent(ctx, entry.Ref, content.bytes)
}

// GetMCPForUninstall 临时保留 Task 8 前的按 Ref definition 获取；Install 禁止调用本方法。
func (c *Client) GetMCPForUninstall(ctx context.Context, ref domain.PowerRef) (domain.MCPServer, error) {
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, err
	}
	if ref.Type != domain.PowerTypeMCP || !safePowerIdentity(ref) || !isCanonicalVersion(ref.Version) {
		return domain.MCPServer{}, cloud.ErrNotFound
	}
	markerPath := filepath.Join(c.resourcesRoot, "mcps", ref.Name, ref.Version, "MCP.json")
	marker, err := os.Lstat(markerPath)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.MCPServer{}, cloud.ErrNotFound
	}
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("stat MCP marker %q: %w", markerPath, err)
	}
	if marker.Mode()&os.ModeSymlink != 0 || !marker.Mode().IsRegular() {
		return domain.MCPServer{}, unsafeContent("MCP marker %q is not a physical regular file", markerPath)
	}
	contents, _, err := capturePhysicalFile(ctx, c.resourcesRoot, markerPath, maxSingleSize, c.beforeSnapshotFileOpen, c.afterSnapshotDataOpen)
	if err != nil {
		return domain.MCPServer{}, err
	}
	return c.decodeMCPContent(ctx, ref, contents)
}

func (c *Client) decodeMCPContent(ctx context.Context, ref domain.PowerRef, contents []byte) (domain.MCPServer, error) {
	resource, err := domain.DecodeMCPResourceJSON(contents)
	if err != nil {
		return domain.MCPServer{}, unsafeContent("decode MCP resource %+v: %v", ref, err)
	}
	if err := resource.ValidatePowerRef(ref); err != nil {
		return domain.MCPServer{}, unsafeContent("validate MCP resource identity %+v: %v", ref, err)
	}
	if c.afterMCPDecode != nil {
		c.afterMCPDecode()
	}
	clone := resource.Definition.Clone()
	if c.afterMCPClone != nil {
		c.afterMCPClone()
	}
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, err
	}
	return clone, nil
}
