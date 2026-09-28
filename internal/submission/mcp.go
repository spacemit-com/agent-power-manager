package submission

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/domain"
)

type mcpSourceLoader struct{}

func (mcpSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	resource, raw, err := readMCPResource(ctx, path)
	if err != nil {
		return source{}, err
	}
	definition := resource.Definition.Clone()
	return source{
		validation: Validation{
			Type: domain.PowerTypeMCP, Name: resource.Name, Version: resource.Version,
			Description: resource.Description, FileCount: 1, ContentSize: int64(len(raw)),
		},
		contentType: "application/json",
		definition:  &definition,
		build: func() ([]byte, error) {
			return CanonicalizeJSON(raw)
		},
	}, nil
}

// readMCPResource 读取并严格解析单个完整 MCP 资源；拒绝未知字段、重复键与尾随值。
// 提交路径额外要求 v2 与必填本地化展示字段（ValidateSubmit），安装与目录解码路径
// 继续使用 DecodeMCPResourceJSON 的 v1+v2 宽松语义（ADR-038）。
func readMCPResource(ctx context.Context, path string) (domain.MCPResource, []byte, error) {
	raw, _, err := readPhysicalRegularFile(ctx, path, maxSingleFileSize)
	if err != nil {
		return domain.MCPResource{}, nil, fmt.Errorf("read MCP file: %w", err)
	}
	resource, err := domain.DecodeMCPResourceJSON(raw)
	if err != nil {
		return domain.MCPResource{}, nil, fmt.Errorf("decode MCP resource: %w", errors.Join(ErrInvalid, err))
	}
	if err := resource.ValidateSubmit(); err != nil {
		return domain.MCPResource{}, nil, fmt.Errorf("decode MCP resource: %w", errors.Join(ErrInvalid, err))
	}
	return resource, append([]byte(nil), raw...), nil
}
