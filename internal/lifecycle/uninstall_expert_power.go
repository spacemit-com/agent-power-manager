package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallExpertPower 解析目标 Agent 的 Adapter，并将文件型卸载交给事务层。
func (s *UninstallService) uninstallExpertPower(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	resolved, err := s.registry.ResolveAgent(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve uninstall adapter for agent %q: %w", agent, err)
	}
	return s.transactions.UninstallAgent(ctx, transaction.AgentUninstallRequest{Agent: agent, Power: ref, Adapter: resolved})
}
