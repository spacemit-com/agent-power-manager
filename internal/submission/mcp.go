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
	// definition 不在这里单独提取：它随 content 的 JCS 字节一起上传，服务端从 content
	// 读取 MCP 定义（ADR-043）。本地校验仍由 readMCPResource 要求它存在。
	return source{
		validation: Validation{
			Type: domain.PowerTypeMCP, SchemaVersion: resource.SchemaVersion,
			Name: resource.Name, Version: resource.Version, Description: resource.Description,
			NameZH: resource.NameZH, NameEN: resource.NameEN, DescriptionZH: resource.DescriptionZH,
			FileCount: 1, ContentSize: int64(len(raw)),
		},
		contentType: "application/json",
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
