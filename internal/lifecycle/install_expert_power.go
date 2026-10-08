package lifecycle

import (
	"context"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// installExpertPower 先核对本地清单与目标 Agent 的现状，按共享状态策略决策后准备 staging
// 并交由事务层发布。
func (s *InstallService) installExpertPower(ctx context.Context, agent domain.AgentID, entry domain.CatalogEntry) (cli.InstallItem, error) {
	ref := entry.Ref
	resolvedAdapter, err := s.registry.ResolveAgent(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve install adapter for agent %q: %w", agent, err)
	}

	previous, installed, err := s.config.Find(agent, ref.Type, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed power %+v for agent %q: %w", ref, agent, err)
	}

	manifest := manifestAbsent
	if installed && previous.Ref.Version == ref.Version {
		manifest = manifestSameVersion
	} else if installed {
		manifest = manifestDifferentVersion
	}
	if manifest == manifestSameVersion {
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeUnchanged}, nil
	}

	presence, err := s.transactions.InspectFilesystemTarget(ref, resolvedAdapter)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect install target for power %+v for agent %q: %w", ref, agent, err)
	}
	target := targetAbsent
	if presence == transaction.TargetPresent {
		target = targetComplete
	}

	decision := decideInstallState(manifest, target)
	switch decision.action {
	case actionInstallNew:
		staging, err := s.cache.PrepareExpert(ctx, entry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("prepare install power %+v: %w", ref, err)
		}
		request := transaction.AgentInstallRequest{
			Agent:         agent,
			Power:         ref,
			Description:   entry.Description,
			Tags:          entry.Tags,
			NameZH:        entry.NameZH,
			NameEN:        entry.NameEN,
			DescriptionZH: entry.DescriptionZH,
			StagingRoot:   staging,
			Adapter:       resolvedAdapter,
		}
		if err := s.transactions.InstallAgent(ctx, request); err != nil {
			return cli.InstallItem{}, fmt.Errorf("install power %+v for agent %q: %w", ref, agent, err)
		}
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeInstalled}, nil
	case actionReplace:
		staging, err := s.cache.PrepareExpert(ctx, entry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("prepare install power %+v: %w", ref, err)
		}
		previousCopy := previous.Ref
		request := transaction.AgentInstallRequest{
			Agent:         agent,
			Power:         ref,
			Description:   entry.Description,
			Tags:          entry.Tags,
			NameZH:        entry.NameZH,
			NameEN:        entry.NameEN,
			DescriptionZH: entry.DescriptionZH,
			Previous:      &previousCopy,
			StagingRoot:   staging,
			Adapter:       resolvedAdapter,
		}
		if err := s.transactions.InstallAgent(ctx, request); err != nil {
			return cli.InstallItem{}, fmt.Errorf("install power %+v for agent %q: %w", ref, agent, err)
		}
		outcome := cli.InstallOutcomeInstalled
		if semver.MustParse(ref.Version).GreaterThan(semver.MustParse(previous.Ref.Version)) {
			outcome = cli.InstallOutcomeUpgraded
		} else {
			outcome = cli.InstallOutcomeDowngraded
		}
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}, nil
	case actionCleanAndInstall:
		if decision.warnMissing {
			logInstallMissingTarget(agent, previous.Ref)
		}
		if _, err := s.transactions.UninstallAgent(ctx, transaction.AgentUninstallRequest{
			Agent:   agent,
			Power:   previous.Ref,
			Adapter: resolvedAdapter,
		}); err != nil {
			return cli.InstallItem{}, fmt.Errorf("repair installed power %+v for agent %q: %w", previous.Ref, agent, err)
		}
		staging, err := s.cache.PrepareExpert(ctx, entry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("prepare install power %+v: %w", ref, err)
		}
		request := transaction.AgentInstallRequest{
			Agent:         agent,
			Power:         ref,
			Description:   entry.Description,
			Tags:          entry.Tags,
			NameZH:        entry.NameZH,
			NameEN:        entry.NameEN,
			DescriptionZH: entry.DescriptionZH,
			StagingRoot:   staging,
			Adapter:       resolvedAdapter,
		}
		if err := s.transactions.InstallAgent(ctx, request); err != nil {
			return cli.InstallItem{}, fmt.Errorf("install power %+v for agent %q: %w", ref, agent, err)
		}
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeInstalled}, nil
	case actionRejectConflict:
		return cli.InstallItem{}, fmt.Errorf("unmanaged target for power %+v for agent %q already exists: %w", ref, agent, adapter.ErrTargetConflict)
	default:
		return cli.InstallItem{}, fmt.Errorf("install power %+v for agent %q: unresolvable entry state", ref, agent)
	}
}
