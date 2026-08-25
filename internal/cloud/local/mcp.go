package local

import (
	"context"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var _ cloud.MCPSource = (*Client)(nil)

// GetMCP 从一次 fresh Local catalog content 读取定义；entry metadata 必须与该物理快照精确一致。
func (c *Client) GetMCP(ctx context.Context, entry domain.CatalogEntry) (domain.MCPServer, error) {
	content, err := c.loadMatchingCatalogContent(ctx, entry, domain.PowerTypeMCP)
	if err != nil {
		return domain.MCPServer{}, err
	}
	return c.decodeMCPContent(ctx, entry.Ref, content.bytes)
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
