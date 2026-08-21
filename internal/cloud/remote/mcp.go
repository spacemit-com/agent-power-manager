package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var _ cloud.MCPSource = (*Client)(nil)

// GetMCP 在私有临时目录中验证精确版本 MCP 内容，严格解码后返回不共享可变切片的领域值。
func (c *Client) GetMCP(ctx context.Context, ref domain.PowerRef) (server domain.MCPServer, resultErr error) {
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("get remote MCP: %w", err)
	}
	if ref.Type != domain.PowerTypeMCP || !validResolveQuery(ref) || ref.Version == "" {
		return domain.MCPServer{}, fmt.Errorf("get invalid remote MCP identity: %w", cloud.ErrNotFound)
	}
	temporaryParent, temporaryDirectory, err := c.createMCPTemporaryDirectory()
	if err != nil {
		return domain.MCPServer{}, err
	}
	var temporary *stagedContent
	defer func() {
		if temporary != nil {
			resultErr = errors.Join(resultErr, temporary.remove())
		}
		if c.beforeMCPCleanup != nil {
			c.beforeMCPCleanup(temporaryDirectory)
		}
		resultErr = errors.Join(resultErr, temporaryDirectory.close(), temporaryParent.close())
	}()
	temporary, err = c.downloadVerifiedContent(ctx, ref, "application/json", temporaryDirectory.directory, 0o600)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("download remote MCP content: %w", err)
	}
	opened, err := temporary.openRead()
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("open verified remote MCP content: %w", err)
	}
	contents, readErr := io.ReadAll(opened)
	closeErr := opened.Close()
	if readErr != nil || closeErr != nil {
		return domain.MCPServer{}, fmt.Errorf("read verified remote MCP content: %w", errors.Join(readErr, closeErr))
	}
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("decode remote MCP: %w", err)
	}
	server, err = domain.DecodeMCPServerJSON(contents)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("validate remote MCP definition: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("return remote MCP: %w", err)
	}
	return server.Clone(), nil
}

func (c *Client) createMCPTemporaryDirectory() (*pinnedDirectory, *ownedDirectory, error) {
	temporaryParentPath := c.mcpTempParent
	productionSystemTemp := temporaryParentPath == ""
	if temporaryParentPath == "" {
		resolved, err := filepath.EvalSymlinks(filepath.Clean(os.TempDir()))
		if err != nil {
			return nil, nil, fmt.Errorf("resolve system remote MCP temporary directory: %w", errors.Join(cloud.ErrUnsafeContent, err))
		}
		temporaryParentPath = resolved
	}
	var temporaryParent *pinnedDirectory
	var err error
	if productionSystemTemp {
		temporaryParent, err = openSystemTemporaryDirectory(temporaryParentPath)
	} else {
		temporaryParent, err = openPinnedDirectory(temporaryParentPath)
	}
	if err != nil {
		return nil, nil, err
	}
	temporaryDirectory, err := temporaryParent.createOwned("apm-remote-mcp-")
	if err != nil {
		return nil, nil, errors.Join(err, temporaryParent.close())
	}
	return temporaryParent, temporaryDirectory, nil
}
