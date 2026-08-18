package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/pluginresource"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// installPlugin 以 manifest 为第一事实来源；精确命中时禁止访问资源、Cloud 或 Agent。
func (s *InstallService) installPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	previous, installed, err := s.config.Find(agent, ref.Type, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed Plugin %+v for agent %q: %w", ref, agent, err)
	}
	if installed && previous == ref {
		return pluginInstallItem(agent, ref, cli.InstallOutcomeUnchanged), nil
	}

	// 目标版本允许 Ensure 下载；旧版本恢复材料只能由后续 Lookup 离线读取。
	targetArtifact, err := s.plugins.Ensure(ctx, agent, ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("ensure target Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	if err := validateLifecyclePluginArtifact(targetArtifact, agent, ref); err != nil {
		return cli.InstallItem{}, err
	}
	pluginAdapter, err := s.registry.ResolvePlugin(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve Plugin adapter for agent %q: %w", agent, err)
	}

	switch pluginAdapter.DeploymentMode() {
	case domain.PluginDeploymentMarketplace:
		marketplaceAdapter, ok := pluginAdapter.(adapter.MarketplacePluginAdapter)
		if !ok {
			return cli.InstallItem{}, fmt.Errorf("resolve marketplace Plugin adapter for agent %q: %w", agent, adapter.ErrUnsupportedAdapter)
		}
		return s.installMarketplacePlugin(ctx, agent, ref, previous, installed, targetArtifact, pluginAdapter, marketplaceAdapter)
	case domain.PluginDeploymentFilesystem:
		return s.installHermesPlugin(ctx, agent, ref, previous, installed, targetArtifact, pluginAdapter)
	default:
		return cli.InstallItem{}, fmt.Errorf("unsupported Plugin deployment %q: %w", pluginAdapter.DeploymentMode(), adapter.ErrUnsupportedAdapter)
	}
}

func (s *InstallService) installMarketplacePlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	previous domain.PowerRef,
	installed bool,
	targetArtifact pluginresource.Artifact,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	if !installed {
		return s.installUnmanagedMarketplacePlugin(ctx, agent, ref, targetArtifact, pluginAdapter, marketplaceAdapter)
	}

	previousName, err := domain.ActualPluginName(previous)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive previous Plugin actual name: %w", err)
	}
	marketplaceName, err := domain.PluginMarketplaceName(previous)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive previous Plugin marketplace name: %w", err)
	}
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, marketplaceName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect previous marketplace for agent %q: %w", agent, err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, previousName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect previous Plugin for agent %q: %w", agent, err)
	}

	marketplaceAbsent := marketplaceFact.Presence == adapter.MarketplaceAbsent
	pluginAbsent := pluginFact.Presence == adapter.PluginAbsent
	if marketplaceAbsent && pluginAbsent {
		if marketplaceFact.Name != marketplaceName || pluginFact.ActualName != previousName {
			return cli.InstallItem{}, fmt.Errorf("absent previous marketplace/Plugin facts do not match manifest: %w", transaction.ErrIndeterminateState)
		}
		if err := s.config.Remove(agent, domain.PowerTypePlugin, ref.Name); err != nil {
			return cli.InstallItem{}, fmt.Errorf("remove stale Plugin manifest for agent %q: %w", agent, err)
		}
		return s.installUnmanagedMarketplacePlugin(ctx, agent, ref, targetArtifact, pluginAdapter, marketplaceAdapter)
	}
	if marketplaceFact.Presence != adapter.MarketplacePresent || pluginFact.Presence != adapter.PluginPresent {
		return cli.InstallItem{}, fmt.Errorf("previous marketplace/Plugin state is partial: %w", transaction.ErrIndeterminateState)
	}

	// 旧版本只允许 Lookup；缺失或损坏时不得回源 Cloud，也不得改动 manifest。
	previousArtifact, err := s.plugins.Lookup(agent, previous)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("lookup previous Plugin artifact %+v for agent %q: %w", previous, agent, err)
	}
	if err := validateLifecyclePluginArtifact(previousArtifact, agent, previous); err != nil {
		return cli.InstallItem{}, err
	}
	previousTarget := marketplacePluginTarget(previousArtifact)
	if marketplaceFact.Name != previousArtifact.MarketplaceName || marketplaceFact.Source != previousArtifact.MarketplaceSource ||
		pluginFact.ActualName != previousTarget.ActualName || pluginFact.Selector != previousTarget.Selector {
		return cli.InstallItem{}, fmt.Errorf("previous marketplace/Plugin state conflicts with manifest: %w", transaction.ErrIndeterminateState)
	}

	targetMarketplace := marketplaceTarget(targetArtifact)
	previousMarketplace := marketplaceTarget(previousArtifact)
	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Previous: powerRefPointer(previous), Adapter: pluginAdapter,
		Target:         marketplacePluginTarget(targetArtifact),
		PreviousTarget: &previousTarget,
		Marketplace: &transaction.PluginMarketplaceInstallRequest{
			Target: targetMarketplace, Previous: &previousMarketplace, Adapter: marketplaceAdapter,
		},
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	return pluginInstallItem(agent, ref, pluginVersionOutcome(previous, ref)), nil
}

func (s *InstallService) installUnmanagedMarketplacePlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	artifact pluginresource.Artifact,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, artifact.MarketplaceName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect target marketplace for agent %q: %w", agent, err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, artifact.ActualName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect target Plugin for agent %q: %w", agent, err)
	}
	if marketplaceFact.Presence == adapter.MarketplacePresent {
		return cli.InstallItem{}, fmt.Errorf("unmanaged marketplace %q already exists: %w", artifact.MarketplaceName, adapter.ErrMarketplaceConflict)
	}
	if marketplaceFact.Presence != adapter.MarketplaceAbsent {
		return cli.InstallItem{}, fmt.Errorf("unexpected target marketplace presence %q: %w", marketplaceFact.Presence, transaction.ErrIndeterminateState)
	}
	if pluginFact.Presence == adapter.PluginPresent {
		return cli.InstallItem{}, fmt.Errorf("unmanaged Plugin %q already exists: %w", artifact.ActualName, errors.Join(adapter.ErrPluginConflict, adapter.ErrTargetConflict))
	}
	if pluginFact.Presence != adapter.PluginAbsent || marketplaceFact.Name != artifact.MarketplaceName || pluginFact.ActualName != artifact.ActualName {
		return cli.InstallItem{}, fmt.Errorf("unexpected target marketplace/Plugin facts: %w", transaction.ErrIndeterminateState)
	}

	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Adapter: pluginAdapter,
		Target: marketplacePluginTarget(artifact),
		Marketplace: &transaction.PluginMarketplaceInstallRequest{
			Target: marketplaceTarget(artifact), Adapter: marketplaceAdapter,
		},
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	return pluginInstallItem(agent, ref, cli.InstallOutcomeInstalled), nil
}

func (s *InstallService) installHermesPlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	previous domain.PowerRef,
	installed bool,
	artifact pluginresource.Artifact,
	pluginAdapter adapter.PluginAdapter,
) (cli.InstallItem, error) {
	if agent != domain.AgentHermes {
		return cli.InstallItem{}, fmt.Errorf("filesystem Plugin adapter for agent %q: %w", agent, adapter.ErrUnsupportedAdapter)
	}
	if installed {
		previousName, err := domain.ActualPluginName(previous)
		if err != nil {
			return cli.InstallItem{}, err
		}
		fact, err := pluginAdapter.Inspect(ctx, previousName)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("inspect previous Hermes Plugin: %w", err)
		}
		switch fact.Presence {
		case adapter.PluginAbsent:
			if fact.ActualName != previousName {
				return cli.InstallItem{}, fmt.Errorf("unexpected previous Hermes Plugin fact: %w", transaction.ErrIndeterminateState)
			}
			if err := s.config.Remove(agent, domain.PowerTypePlugin, ref.Name); err != nil {
				return cli.InstallItem{}, fmt.Errorf("remove stale Hermes Plugin manifest: %w", err)
			}
			installed = false
		case adapter.PluginPresent:
			if fact.ActualName != previousName || fact.Selector != previousName {
				return cli.InstallItem{}, fmt.Errorf("previous Hermes Plugin conflicts with manifest: %w", transaction.ErrIndeterminateState)
			}
		default:
			return cli.InstallItem{}, fmt.Errorf("unexpected previous Hermes Plugin presence %q: %w", fact.Presence, transaction.ErrIndeterminateState)
		}
	}
	if !installed {
		fact, err := pluginAdapter.Inspect(ctx, artifact.ActualName)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("inspect target Hermes Plugin: %w", err)
		}
		if fact.Presence == adapter.PluginPresent {
			return cli.InstallItem{}, fmt.Errorf("unmanaged Hermes Plugin %q already exists: %w", artifact.ActualName, errors.Join(adapter.ErrPluginConflict, adapter.ErrTargetConflict))
		}
		if fact.Presence != adapter.PluginAbsent || fact.ActualName != artifact.ActualName {
			return cli.InstallItem{}, fmt.Errorf("unexpected target Hermes Plugin fact: %w", transaction.ErrIndeterminateState)
		}
	}

	// StageHermes 仅在所有事实校验完成、即将进入发布事务时调用。
	staging, err := s.plugins.StageHermes(ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("stage Hermes Plugin artifact %+v: %w", ref, err)
	}
	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, StagingRoot: staging, Adapter: pluginAdapter,
		Target: adapter.PluginTarget{ActualName: artifact.ActualName, Selector: artifact.ActualName},
	}
	if installed {
		request.Previous = powerRefPointer(previous)
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	outcome := cli.InstallOutcomeInstalled
	if installed {
		outcome = pluginVersionOutcome(previous, ref)
	}
	return pluginInstallItem(agent, ref, outcome), nil
}

func validateLifecyclePluginArtifact(artifact pluginresource.Artifact, agent domain.AgentID, ref domain.PowerRef) error {
	if err := artifact.Validate(); err != nil {
		return fmt.Errorf("validate Plugin artifact %+v: %w", ref, err)
	}
	expectedName, err := domain.ActualPluginName(ref)
	if err != nil {
		return err
	}
	if artifact.Agent != agent || artifact.Power != ref || artifact.ActualName != expectedName {
		return fmt.Errorf("Plugin artifact identity %+v does not match agent %q ref %+v: %w", artifact, agent, ref, pluginresource.ErrInvalidArtifact)
	}
	return nil
}

func marketplaceTarget(artifact pluginresource.Artifact) adapter.MarketplaceTarget {
	return adapter.MarketplaceTarget{Name: artifact.MarketplaceName, Source: artifact.MarketplaceSource}
}

func marketplacePluginTarget(artifact pluginresource.Artifact) adapter.PluginTarget {
	return adapter.PluginTarget{ActualName: artifact.ActualName, Selector: artifact.ActualName + "@" + artifact.MarketplaceName}
}

func powerRefPointer(ref domain.PowerRef) *domain.PowerRef {
	copied := ref
	return &copied
}

func pluginInstallItem(agent domain.AgentID, ref domain.PowerRef, outcome cli.InstallOutcome) cli.InstallItem {
	return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}
}

func pluginVersionOutcome(previous, target domain.PowerRef) cli.InstallOutcome {
	if semver.MustParse(target.Version).GreaterThan(semver.MustParse(previous.Version)) {
		return cli.InstallOutcomeUpgraded
	}
	return cli.InstallOutcomeDowngraded
}
