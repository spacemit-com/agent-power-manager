package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallMCP 先用目标 Agent CLI 确认状态，仅在注册存在时读取精确版本定义。
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
		server, err := s.cloud.GetMCP(ctx, ref)
		if err != nil {
			return transaction.UninstallResult{}, fmt.Errorf("get MCP %+v from Cloud for uninstall: %w", ref, err)
		}
		request.Server = mcpServerPointer(server)
	case adapter.MCPAbsent:
	default:
		return transaction.UninstallResult{}, fmt.Errorf("unexpected MCP presence %q: %w", presence, transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallMCP(ctx, request)
}
