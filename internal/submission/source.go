package submission

import (
	"context"
	"fmt"

	"apm/internal/domain"
)

// sourceLoader 统一加载并完整校验一种提交资源；实现必须保持离线且只读取指定路径。
type sourceLoader interface {
	Load(context.Context, string) (source, error)
}

// source 保存已校验身份以及延迟构建内容的闭包，供 Validate 与 Prepare 共用加载流程。
type source struct {
	validation  Validation
	contentType string
	build       func() ([]byte, error)
}

func loaderFor(powerType domain.PowerType) (sourceLoader, error) {
	switch powerType {
	case domain.PowerTypeSkill:
		return skillSourceLoader{}, nil
	case domain.PowerTypePlugin:
		return pluginSourceLoader{}, nil
	case domain.PowerTypeMCP:
		return mcpSourceLoader{}, nil
	case domain.PowerTypeAgent:
		return agentSourceLoader{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %q: %w", powerType, ErrInvalid)
	}
}

func loadSource(ctx context.Context, powerType domain.PowerType, path string) (source, error) {
	loader, err := loaderFor(powerType)
	if err != nil {
		return source{}, err
	}
	return loader.Load(ctx, path)
}
