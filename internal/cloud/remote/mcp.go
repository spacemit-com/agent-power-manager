package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
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
	temporaryDirectory, err := c.createMCPTemporaryDirectory()
	if err != nil {
		return domain.MCPServer{}, err
	}
	defer func() {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove owned remote MCP temporary directory %q: %w", temporaryDirectory, err))
		}
	}()
	temporary, err := c.downloadVerifiedContent(ctx, ref, "application/json", temporaryDirectory)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("download remote MCP content: %w", err)
	}
	contents, err := os.ReadFile(temporary)
	if err != nil {
		return domain.MCPServer{}, fmt.Errorf("read verified remote MCP content %q: %w", temporary, err)
	}
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("decode remote MCP: %w", err)
	}
	if err := jsonstrict.Decode(contents, &server, true); err != nil {
		return domain.MCPServer{}, fmt.Errorf("decode remote MCP definition: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if err := server.Validate(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("validate remote MCP definition: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if err := ctx.Err(); err != nil {
		return domain.MCPServer{}, fmt.Errorf("return remote MCP: %w", err)
	}
	return server.Clone(), nil
}

func (c *Client) createMCPTemporaryDirectory() (string, error) {
	if c.mcpTempParent != "" {
		if err := validateRemotePhysicalDirectory(c.mcpTempParent); err != nil {
			return "", err
		}
	}
	temporaryDirectory, err := os.MkdirTemp(c.mcpTempParent, "apm-remote-mcp-")
	if err != nil {
		return "", fmt.Errorf("create remote MCP temporary directory: %w", err)
	}
	if err := validateRemotePhysicalDirectory(filepath.Clean(temporaryDirectory)); err != nil {
		removeErr := os.RemoveAll(temporaryDirectory)
		return "", errors.Join(err, removeErr)
	}
	return temporaryDirectory, nil
}
