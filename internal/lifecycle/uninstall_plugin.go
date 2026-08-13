package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallPlugin 只依赖本地清单和 Agent 事实，禁止连接 Cloud 或更新 marketplace。
func (s *UninstallService) uninstallPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	pluginAdapter, err := s.registry.ResolvePlugin(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve Plugin adapter for agent %q: %w", agent, err)
	}
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("derive installed Plugin actual name: %w", err)
	}
	fact, err := pluginAdapter.Inspect(ctx, actualName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed Plugin %q: %w", actualName, err)
	}
	if fact.Presence != adapter.PluginPresent && fact.Presence != adapter.PluginAbsent {
		return transaction.UninstallResult{}, fmt.Errorf("unexpected Plugin presence %q: %w", fact.Presence, transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
		Agent: agent, Power: ref, ActualName: actualName, Adapter: pluginAdapter,
	})
}
