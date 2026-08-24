package submission

import (
	"context"
	"errors"
	"fmt"
	"os"

	"apm/internal/domain"
)

type mcpSourceLoader struct{}

func (mcpSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	resource, raw, err := readMCPResource(path)
	if err != nil {
		return source{}, err
	}
	return source{
		validation: Validation{
			Type: domain.PowerTypeMCP, Name: resource.Name, Version: resource.Version,
			Description: resource.Description, FileCount: 1, ContentSize: int64(len(raw)),
		},
		contentType: "application/json",
		build: func() ([]byte, error) {
			return CanonicalizeJSON(raw)
		},
	}, nil
}

// readMCPResource 读取并严格解析单个完整 MCP v1 资源；拒绝未知字段、重复键与尾随值。
func readMCPResource(path string) (domain.MCPResource, []byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return domain.MCPResource{}, nil, fmt.Errorf("read MCP file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return domain.MCPResource{}, nil, fmt.Errorf("MCP path %q is not a regular file: %w", path, ErrInvalid)
	}
	if info.Size() > maxSingleFileSize {
		return domain.MCPResource{}, nil, fmt.Errorf("MCP file exceeds %d bytes: %w", maxSingleFileSize, ErrInvalid)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return domain.MCPResource{}, nil, fmt.Errorf("read MCP file: %w", err)
	}
	resource, err := domain.DecodeMCPResourceJSON(raw)
	if err != nil {
		return domain.MCPResource{}, nil, fmt.Errorf("decode MCP resource: %w", errors.Join(ErrInvalid, err))
	}
	return resource, append([]byte(nil), raw...), nil
}
