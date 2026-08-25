package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallMCP 只用本地清单和目标 Agent CLI 状态卸载；不得访问 Cloud 或 catalog。
func (s *UninstallService) uninstallMCP(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	mcpAdapter, err := s.registry.ResolveMCP(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve MCP uninstall adapter for agent %q: %w", agent, err)
	}
	presence, err := mcpAdapter.Inspect(ctx, ref.Name)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect MCP %+v for agent %q: %w", ref, agent, err)
	}
	request := transaction.MCPUninstallRequest{Agent: agent, Power: ref, Adapter: mcpAdapter}
	switch presence {
	case adapter.MCPPresent:
	case adapter.MCPAbsent:
	default:
		return transaction.UninstallResult{}, fmt.Errorf("unexpected MCP presence %q: %w", presence, transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallMCP(ctx, request)
}
