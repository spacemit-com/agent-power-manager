package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// installSkill 先核对本地清单，再准备 staging 并交由 Skill 事务完成发布和结果分类。
func (s *InstallService) installSkill(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	resolvedAdapter, err := s.registry.ResolveSkill(agent)
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

	staging, err := s.cache.PrepareSkill(ctx, ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("prepare install power %+v: %w", ref, err)
	}

	request := transaction.SkillInstallRequest{
		Agent:       agent,
		Power:       ref,
		StagingRoot: staging,
		Adapter:     resolvedAdapter,
	}
	if installed {
		request.Previous = &previous
	}
	if err := s.transactions.InstallSkill(ctx, request); err != nil {
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
