package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// fetchSkillInstallStaging 按“缓存、Cloud、缓存”的既定顺序准备 Skill staging。
func (s *InstallService) fetchSkillInstallStaging(ref domain.PowerRef) (string, error) {
	staging, err := s.cache.StageSkill(ref)
	if err == nil {
		return staging, nil
	}
	if !errors.Is(err, cache.ErrMiss) {
		return "", fmt.Errorf("fetch install power %+v from cache: %w", ref, err)
	}
	if err := s.cloud.DownloadSkill(ref); err != nil {
		return "", fmt.Errorf("get install power %+v from Cloud: %w", ref, err)
	}
	staging, err = s.cache.StageSkill(ref)
	if err != nil {
		return "", fmt.Errorf("fetch downloaded install power %+v from cache: %w", ref, err)
	}
	return staging, nil
}

func (s *InstallService) installOne(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	switch ref.Type {
	case domain.PowerTypeSkill:
		return s.installSkill(ctx, agent, ref)
	case domain.PowerTypeMCP:
		return s.installMCP(ctx, agent, ref)
	case domain.PowerTypePlugin:
		return s.installPlugin(ctx, agent, ref)
	default:
		return cli.InstallItem{}, fmt.Errorf("install power type %q: %w", ref.Type, adapter.ErrUnsupportedAdapter)
	}
}

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

	staging, err := s.fetchSkillInstallStaging(ref)
	if err != nil {
		return cli.InstallItem{}, err
	}

	request := transaction.InstallRequest{
		Agent:       agent,
		Power:       ref,
		StagingRoot: staging,
		Adapter:     resolvedAdapter,
	}
	if installed {
		request.Previous = &previous
	}
	if err := s.transactions.Install(ctx, request); err != nil {
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
