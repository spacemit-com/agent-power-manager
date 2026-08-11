package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

func (s *InstallService) fetchInstallStaging(ref domain.PowerRef) (string, error) {
	staging, err := s.cache.Fetch(ref)
	if err == nil {
		return staging, nil
	}
	if !errors.Is(err, cache.ErrMiss) {
		return "", fmt.Errorf("fetch install power %+v from cache: %w", ref, err)
	}
	if err := s.cloud.Get(ref); err != nil {
		return "", fmt.Errorf("get install power %+v from Cloud: %w", ref, err)
	}
	staging, err = s.cache.Fetch(ref)
	if err != nil {
		return "", fmt.Errorf("fetch downloaded install power %+v from cache: %w", ref, err)
	}
	return staging, nil
}

func (s *InstallService) installOne(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	resolvedAdapter, err := s.registry.Resolve(agent, ref.Type)
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

	staging, err := s.fetchInstallStaging(ref)
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
