package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallSkill 解析 Skill Adapter，并将文件型卸载交给事务层。
func (s *UninstallService) uninstallSkill(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	resolved, err := s.registry.ResolveSkill(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve uninstall adapter for agent %q: %w", agent, err)
	}
	return s.transactions.Uninstall(ctx, transaction.UninstallRequest{Agent: agent, Power: ref, Adapter: resolved})
}
