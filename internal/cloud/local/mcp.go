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
	"apm/internal/utils/jsonstrict"
)

// GetMCP 直接读取精确版本的 MCP 定义，不写入 Cache 或 staging，读取前必须响应取消。
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
	server, err := decodeMCPServer(contents)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("decode MCP definition %q: %w", markerPath, err)
	}
	if c.afterMCPDecode != nil {
		c.afterMCPDecode()
	}
	clone := server.Clone()
	if c.afterMCPClone != nil {
		c.afterMCPClone()
	}
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, err
	}
	return clone, nil
}

func decodeMCPServer(contents []byte) (domain.MCPServer, error) {
	var server domain.MCPServer
	if err := jsonstrict.Decode(contents, &server, true); err != nil {
		return domain.MCPServer{}, err
	}
	if err := server.Validate(); err != nil {
		return domain.MCPServer{}, err
	}
	return server, nil
}
