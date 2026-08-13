package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// installPlugin 先准备 Cloud marketplace，再按 Agent 事实决定是否进入类型专属事务。
func (s *InstallService) installPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	pluginAdapter, err := s.registry.ResolvePlugin(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve Plugin adapter for agent %q: %w", agent, err)
	}
	previous, installed, err := s.config.Find(agent, ref.Type, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed Plugin %+v for agent %q: %w", ref, agent, err)
	}
	definition, err := s.cloud.GetPlugin(ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("get Plugin %+v from Cloud: %w", ref, err)
	}
	if err := pluginAdapter.PrepareInstall(ctx, definition); err != nil {
		return cli.InstallItem{}, fmt.Errorf("prepare Plugin marketplace for agent %q: %w", agent, err)
	}
	targetFact, err := pluginAdapter.Inspect(ctx, definition.ActualName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect Plugin target for agent %q: %w", agent, err)
	}
	if targetFact.Presence == adapter.PluginPresent {
		if installed && previous.Version == ref.Version {
			return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeUnchanged}, nil
		}
		return cli.InstallItem{}, fmt.Errorf("Plugin target %q already exists: %w", definition.ActualName, errors.Join(adapter.ErrPluginConflict, adapter.ErrTargetConflict))
	}
	if targetFact.Presence != adapter.PluginAbsent {
		return cli.InstallItem{}, fmt.Errorf("unexpected Plugin target state %q: %w", targetFact.Presence, transaction.ErrIndeterminateState)
	}
	if installed && previous.Version != ref.Version {
		previousName, err := domain.ActualPluginName(previous)
		if err != nil {
			return cli.InstallItem{}, err
		}
		if _, err := pluginAdapter.Inspect(ctx, previousName); err != nil {
			return cli.InstallItem{}, fmt.Errorf("inspect previous Plugin for agent %q: %w", agent, err)
		}
	}
	request := transaction.PluginInstallRequest{Agent: agent, Power: ref, Definition: definition, Adapter: pluginAdapter}
	if installed {
		copied := previous
		request.Previous = &copied
	}
	if pluginAdapter.DeploymentMode() == domain.PluginDeploymentFilesystem {
		staging, err := s.fetchInstallStaging(ref)
		if err != nil {
			return cli.InstallItem{}, err
		}
		request.StagingRoot = staging
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	outcome := cli.InstallOutcomeInstalled
	if installed {
		if previous.Version == ref.Version {
			outcome = cli.InstallOutcomeUnchanged
		} else if semver.MustParse(ref.Version).GreaterThan(semver.MustParse(previous.Version)) {
			outcome = cli.InstallOutcomeUpgraded
		} else {
			outcome = cli.InstallOutcomeDowngraded
		}
	}
	return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}, nil
}
