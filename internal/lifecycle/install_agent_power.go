package lifecycle

import (
	"context"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// installAgentPower 先核对本地清单，再用权威目录条目准备 staging 并交由 Agent 事务发布。
func (s *InstallService) installAgentPower(ctx context.Context, agent domain.AgentID, entry domain.CatalogEntry) (cli.InstallItem, error) {
	ref := entry.Ref
	resolvedAdapter, err := s.registry.ResolveAgent(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve install adapter for agent %q: %w", agent, err)
	}

	previous, installed, err := s.config.Find(agent, ref.Type, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed power %+v for agent %q: %w", ref, agent, err)
	}
	if installed && previous.Version == ref.Version {
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeUnchanged}, nil
	}

	staging, err := s.cache.PrepareAgent(ctx, entry)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("prepare install power %+v: %w", ref, err)
	}

	request := transaction.AgentInstallRequest{
		Agent:       agent,
		Power:       ref,
		StagingRoot: staging,
		Adapter:     resolvedAdapter,
	}
	if installed {
		request.Previous = &previous
	}
	if err := s.transactions.InstallAgent(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install power %+v for agent %q: %w", ref, agent, err)
	}

	outcome := cli.InstallOutcomeInstalled
	if installed {
		if semver.MustParse(ref.Version).GreaterThan(semver.MustParse(previous.Version)) {
			outcome = cli.InstallOutcomeUpgraded
		} else {
			outcome = cli.InstallOutcomeDowngraded
		}
	}
	return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}, nil
}
