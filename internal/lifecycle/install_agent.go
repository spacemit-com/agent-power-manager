package lifecycle

import (
	"context"
	"fmt"
	"log/slog"

	"apm/internal/adapter"
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
)

// installForAgent 将同一快照解析出的权威条目分派到对应类型；仅事务与适配器接收 Ref。
func (s *InstallService) installForAgent(ctx context.Context, agent domain.AgentID, entry domain.CatalogEntry, snapshot catalog.Snapshot) (cli.InstallItem, error) {
	ref := entry.Ref
	switch ref.Type {
	case domain.PowerTypeSkill:
		return s.installSkill(ctx, agent, entry)
	case domain.PowerTypeMCP:
		return s.installMCP(ctx, agent, entry, snapshot)
	case domain.PowerTypePlugin:
		return s.installPlugin(ctx, agent, entry)
	case domain.PowerTypeExpert:
		return s.installExpertPower(ctx, agent, entry)
	default:
		return cli.InstallItem{}, fmt.Errorf("install power type %q: %w", ref.Type, adapter.ErrUnsupportedAdapter)
	}
}

// logInstallMissingTarget 记录一次受管安装目标缺失的告警，携带旧清单版本。
func logInstallMissingTarget(agent domain.AgentID, ref domain.PowerRef) {
	slog.Warn("installed power target is missing",
		slog.String("operation", "install"), logging.Agent(agent),
		slog.String("type", string(ref.Type)), logging.Power(ref.Name), logging.Version(ref.Version))
}
