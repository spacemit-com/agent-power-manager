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
	runtimeName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive Plugin runtime name: %w", err)
	}
	target := adapter.PluginTarget{ActualName: runtimeName, Selector: runtimeName}

	// 目标版本允许 Ensure 下载；旧版本恢复材料只能由后续 Lookup 离线读取。
	targetArtifact, err := s.plugins.Ensure(ctx, ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("ensure target Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	if err := validateLifecyclePluginArtifact(targetArtifact, ref); err != nil {
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
		marketplace, err := targetArtifact.MarketplaceFor(agent)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("resolve target Plugin marketplace for agent %q: %w", agent, err)
		}
		return s.installMarketplacePlugin(ctx, agent, ref, previous, installed, target, marketplaceTarget(marketplace), pluginAdapter, marketplaceAdapter)
	case domain.PluginDeploymentFilesystem:
		return s.installFilesystemPlugin(ctx, agent, ref, previous, installed, target, pluginAdapter)
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
	target adapter.PluginTarget,
	targetMarketplace adapter.MarketplaceTarget,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	if !installed {
		return s.installUnmanagedMarketplacePlugin(ctx, agent, ref, target, targetMarketplace, pluginAdapter, marketplaceAdapter)
	}

	previousName, err := domain.PluginRuntimeName(previous)
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
		return s.installUnmanagedMarketplacePlugin(ctx, agent, ref, target, targetMarketplace, pluginAdapter, marketplaceAdapter)
	}
	if marketplaceFact.Presence != adapter.MarketplacePresent || pluginFact.Presence != adapter.PluginPresent {
		return cli.InstallItem{}, fmt.Errorf("previous marketplace/Plugin state is partial: %w", transaction.ErrIndeterminateState)
	}

	// 旧版本只允许 Lookup；缺失或损坏时不得回源 Cloud，也不得改动 manifest。
	previousArtifact, err := s.plugins.Lookup(previous)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("lookup previous Plugin artifact %+v for agent %q: %w", previous, agent, err)
	}
	if err := validateLifecyclePluginArtifact(previousArtifact, previous); err != nil {
		return cli.InstallItem{}, err
	}
	previousMarketplaceResource, err := previousArtifact.MarketplaceFor(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve previous Plugin marketplace for agent %q: %w", agent, err)
	}
	previousMarketplace := marketplaceTarget(previousMarketplaceResource)
	previousTarget := adapter.PluginTarget{ActualName: previousName, Selector: previousName}
	if marketplaceFact.Name != previousMarketplace.Name || marketplaceFact.Source != previousMarketplace.Source ||
		pluginFact.ActualName != previousTarget.ActualName || pluginFact.Selector != previousTarget.Selector {
		return cli.InstallItem{}, fmt.Errorf("previous marketplace/Plugin state conflicts with manifest: %w", transaction.ErrIndeterminateState)
	}

	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Previous: powerRefPointer(previous), Adapter: pluginAdapter,
		Target:         target,
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
	target adapter.PluginTarget,
	marketplace adapter.MarketplaceTarget,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, marketplace.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect target marketplace for agent %q: %w", agent, err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, target.ActualName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect target Plugin for agent %q: %w", agent, err)
	}
	// 正常卸载会保留 marketplace；仅允许复用名称与缓存来源都精确匹配、且 Plugin 缺失的状态。
	retainedMarketplace := marketplaceFact.Presence == adapter.MarketplacePresent &&
		marketplaceFact.Name == marketplace.Name && marketplaceFact.Source == marketplace.Source
	if marketplaceFact.Presence == adapter.MarketplacePresent && !retainedMarketplace {
		return cli.InstallItem{}, fmt.Errorf("unmanaged marketplace %q already exists: %w", marketplace.Name, adapter.ErrMarketplaceConflict)
	}
	if marketplaceFact.Presence != adapter.MarketplaceAbsent && !retainedMarketplace {
		return cli.InstallItem{}, fmt.Errorf("unexpected target marketplace presence %q: %w", marketplaceFact.Presence, transaction.ErrIndeterminateState)
	}
	if pluginFact.Presence == adapter.PluginPresent {
		return cli.InstallItem{}, fmt.Errorf("unmanaged Plugin %q already exists: %w", target.ActualName, errors.Join(adapter.ErrPluginConflict, adapter.ErrTargetConflict))
	}
	if pluginFact.Presence != adapter.PluginAbsent || marketplaceFact.Name != marketplace.Name || pluginFact.ActualName != target.ActualName {
		return cli.InstallItem{}, fmt.Errorf("unexpected target marketplace/Plugin facts: %w", transaction.ErrIndeterminateState)
	}

	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Adapter: pluginAdapter,
		Target: target,
		Marketplace: &transaction.PluginMarketplaceInstallRequest{
			Target: marketplace, Adapter: marketplaceAdapter,
		},
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	return pluginInstallItem(agent, ref, cli.InstallOutcomeInstalled), nil
}

// installFilesystemPlugin 对任意 filesystem Agent 复用同一逻辑目标；staging 只能在本地事实校验通过后生成。
func (s *InstallService) installFilesystemPlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	previous domain.PowerRef,
	installed bool,
	target adapter.PluginTarget,
	pluginAdapter adapter.PluginAdapter,
) (cli.InstallItem, error) {
	if installed {
		previousName, err := domain.PluginRuntimeName(previous)
		if err != nil {
			return cli.InstallItem{}, err
		}
		fact, err := pluginAdapter.Inspect(ctx, previousName)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("inspect previous filesystem Plugin: %w", err)
		}
		switch fact.Presence {
		case adapter.PluginAbsent:
			if fact.ActualName != previousName {
				return cli.InstallItem{}, fmt.Errorf("unexpected previous filesystem Plugin fact: %w", transaction.ErrIndeterminateState)
			}
			if err := s.config.Remove(agent, domain.PowerTypePlugin, ref.Name); err != nil {
				return cli.InstallItem{}, fmt.Errorf("remove stale filesystem Plugin manifest: %w", err)
			}
			installed = false
		case adapter.PluginPresent:
			if fact.ActualName != previousName || fact.Selector != previousName {
				return cli.InstallItem{}, fmt.Errorf("previous filesystem Plugin conflicts with manifest: %w", transaction.ErrIndeterminateState)
			}
		default:
			return cli.InstallItem{}, fmt.Errorf("unexpected previous filesystem Plugin presence %q: %w", fact.Presence, transaction.ErrIndeterminateState)
		}
	}
	if !installed {
		fact, err := pluginAdapter.Inspect(ctx, target.ActualName)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("inspect target filesystem Plugin: %w", err)
		}
		if fact.Presence == adapter.PluginPresent {
			return cli.InstallItem{}, fmt.Errorf("unmanaged filesystem Plugin %q already exists: %w", target.ActualName, errors.Join(adapter.ErrPluginConflict, adapter.ErrTargetConflict))
		}
		if fact.Presence != adapter.PluginAbsent || fact.ActualName != target.ActualName {
			return cli.InstallItem{}, fmt.Errorf("unexpected target filesystem Plugin fact: %w", transaction.ErrIndeterminateState)
		}
	}

	// 通用 filesystem staging 仅在所有事实校验完成、即将进入发布事务时调用。
	staging, err := s.plugins.StageFilesystem(ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("stage filesystem Plugin artifact %+v: %w", ref, err)
	}
	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, StagingRoot: staging, Adapter: pluginAdapter,
		Target: target,
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

func validateLifecyclePluginArtifact(artifact pluginresource.Artifact, ref domain.PowerRef) error {
	if err := artifact.Validate(); err != nil {
		return fmt.Errorf("validate Plugin artifact %+v: %w", ref, err)
	}
	if artifact.Power != ref {
		return fmt.Errorf("Plugin artifact identity %+v does not match ref %+v: %w", artifact, ref, pluginresource.ErrInvalidArtifact)
	}
	return nil
}

func marketplaceTarget(marketplace pluginresource.Marketplace) adapter.MarketplaceTarget {
	return adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.Source}
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
