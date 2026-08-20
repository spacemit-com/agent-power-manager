package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallAgentPower 解析 Agent Adapter，并将文件型卸载交给事务层。
func (s *UninstallService) uninstallAgentPower(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	resolved, err := s.registry.ResolveAgent(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve uninstall adapter for agent %q: %w", agent, err)
	}
	return s.transactions.UninstallAgent(ctx, transaction.AgentUninstallRequest{Agent: agent, Power: ref, Adapter: resolved})
}
