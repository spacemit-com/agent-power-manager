package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
)

// installForAgent 将已解析的 Power 分派到对应类型处理器。
func (s *InstallService) installForAgent(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	switch ref.Type {
	case domain.PowerTypeSkill:
		return s.installSkill(ctx, agent, ref)
	case domain.PowerTypeMCP:
		return s.installMCP(ctx, agent, ref)
	case domain.PowerTypePlugin:
		return s.installPlugin(ctx, agent, ref)
	default:
		return cli.InstallItem{}, fmt.Errorf("install power type %q: %w", ref.Type, adapter.ErrUnsupportedAdapter)
	}
}
