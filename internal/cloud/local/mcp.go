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

// GetMCP 直接读取精确版本的完整 MCP 资源；文件内声明的身份必须与 ref 一致。
func (c *Client) GetMCP(ctx context.Context, ref domain.PowerRef) (domain.MCPServer, error) {
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
	resource, err := domain.DecodeMCPResourceJSON(contents)
	if err != nil {
		return domain.MCPServer{}, unsafeContent("decode MCP resource %q: %v", markerPath, err)
	}
	if err := resource.ValidatePowerRef(ref); err != nil {
		return domain.MCPServer{}, unsafeContent("validate MCP resource identity %q: %v", markerPath, err)
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
