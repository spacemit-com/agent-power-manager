package lifecycle

import (
	"context"
	"fmt"
	"log/slog"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

// uninstallOne 解析并执行单个 Agent 的卸载；返回项中的 Power 始终使用独立副本。
func (s *UninstallService) uninstallOne(ctx context.Context, agent domain.AgentID, flags cli.Flags) (cli.UninstallItem, error) {
	groups, err := s.config.List(agent)
	if err != nil {
		return cli.UninstallItem{Agent: agent, Outcome: cli.UninstallOutcomeFailed},
			fmt.Errorf("list installed powers for agent %q: %w", agent, err)
	}
	ref, found, err := resolveUninstallPower(flags, groups)
	if err != nil {
		return cli.UninstallItem{Agent: agent, Outcome: cli.UninstallOutcomeFailed}, err
	}
	if !found {
		logUninstallNotInstalled(agent, flags)
		return cli.UninstallItem{Agent: agent, Outcome: cli.UninstallOutcomeUnexecuted}, nil
	}

	copied := ref
	item := cli.UninstallItem{Agent: agent, Power: &copied}
	var transactionResult transaction.UninstallResult
	switch ref.Type {
	case domain.PowerTypeSkill:
		transactionResult, err = s.uninstallSkill(ctx, agent, ref)
	case domain.PowerTypeMCP:
		transactionResult, err = s.uninstallMCP(ctx, agent, ref)
	case domain.PowerTypePlugin:
		transactionResult, err = s.uninstallPlugin(ctx, agent, ref)
	default:
		err = fmt.Errorf("uninstall power type %q: %w", ref.Type, adapter.ErrUnsupportedAdapter)
	}
	if transactionResult.TargetMissing {
		logUninstallMissingTarget(agent, ref)
	}
	if err != nil {
		item.Outcome = cli.UninstallOutcomeFailed
		return item, fmt.Errorf("uninstall power %+v for agent %q: %w", ref, agent, err)
	}
	item.Outcome = cli.UninstallOutcomeCompleted
	return item, nil
}

func (s *UninstallService) uninstallSkill(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	resolved, err := s.registry.ResolveSkill(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve uninstall adapter for agent %q: %w", agent, err)
	}
	return s.transactions.Uninstall(ctx, transaction.UninstallRequest{Agent: agent, Power: ref, Adapter: resolved})
}

func logUninstallNotInstalled(agent domain.AgentID, flags cli.Flags) {
	attributes := []any{
		slog.String("operation", "uninstall"),
		logging.Agent(agent),
		logging.Power(flags.Name),
	}
	if flags.Type != "" {
		attributes = append(attributes, slog.String("type", flags.Type))
	}
	slog.Warn("power is not installed", attributes...)
}

func logUninstallMissingTarget(agent domain.AgentID, ref domain.PowerRef) {
	slog.Warn(
		"installed power target is missing",
		slog.String("operation", "uninstall"),
		logging.Agent(agent),
		logging.Power(ref.Name),
		slog.String("type", string(ref.Type)),
		logging.Version(ref.Version),
	)
}
