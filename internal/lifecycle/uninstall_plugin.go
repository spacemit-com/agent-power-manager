package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallPlugin 只依据 manifest 指向的精确版本检查本地事实；所有分支均禁止连接 Cloud。
func (s *UninstallService) uninstallPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	pluginAdapter, err := s.registry.ResolvePlugin(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve Plugin adapter for agent %q: %w", agent, err)
	}
	actualName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("derive installed Plugin actual name: %w", err)
	}

	switch pluginAdapter.DeploymentMode() {
	case domain.PluginDeploymentMarketplace:
		marketplaceAdapter, ok := pluginAdapter.(adapter.MarketplacePluginAdapter)
		if !ok {
			return transaction.UninstallResult{}, fmt.Errorf("resolve marketplace Plugin adapter for agent %q: %w", agent, adapter.ErrUnsupportedAdapter)
		}
		return s.uninstallMarketplacePlugin(ctx, agent, ref, actualName, pluginAdapter, marketplaceAdapter)
	case domain.PluginDeploymentFilesystem:
		return s.uninstallFilesystemPlugin(ctx, agent, ref, actualName, pluginAdapter)
	default:
		return transaction.UninstallResult{}, fmt.Errorf("unsupported Plugin deployment %q: %w", pluginAdapter.DeploymentMode(), adapter.ErrUnsupportedAdapter)
	}
}

func (s *UninstallService) uninstallMarketplacePlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	actualName string,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (transaction.UninstallResult, error) {
	marketplaceName, err := domain.PluginMarketplaceName(ref)
	if err != nil {
		return transaction.UninstallResult{}, err
	}
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, marketplaceName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed Plugin marketplace: %w", err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, actualName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed Plugin %q: %w", actualName, err)
	}

	marketplaceAbsent := marketplaceFact.Presence == adapter.MarketplaceAbsent
	pluginAbsent := pluginFact.Presence == adapter.PluginAbsent
	if marketplaceAbsent && pluginAbsent {
		if marketplaceFact.Name != marketplaceName || pluginFact.ActualName != actualName {
			return transaction.UninstallResult{}, fmt.Errorf("missing marketplace/Plugin facts do not match manifest: %w", transaction.ErrIndeterminateState)
		}
		result := transaction.UninstallResult{TargetMissing: true}
		if err := s.config.Remove(agent, domain.PowerTypePlugin, ref.Name); err != nil {
			return result, fmt.Errorf("remove missing Plugin manifest entry: %w", err)
		}
		return result, nil
	}
	if marketplaceFact.Presence != adapter.MarketplacePresent || pluginFact.Presence != adapter.PluginPresent {
		return transaction.UninstallResult{}, fmt.Errorf("installed marketplace/Plugin state is partial: %w", transaction.ErrIndeterminateState)
	}

	// Uninstall 必须离线 Lookup manifest 精确版本；缺失或损坏时保留 manifest 并 fail closed。
	artifact, err := s.plugins.Lookup(ref)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("lookup installed Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	if err := validateLifecyclePluginArtifact(artifact, ref); err != nil {
		return transaction.UninstallResult{}, err
	}
	marketplaceResource, err := artifact.MarketplaceFor(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve installed Plugin marketplace for agent %q: %w", agent, err)
	}
	target := adapter.PluginTarget{ActualName: actualName, Selector: actualName}
	marketplace := marketplaceTarget(marketplaceResource)
	if marketplaceFact.Name != marketplace.Name || marketplaceFact.Source != marketplace.Source ||
		pluginFact.ActualName != target.ActualName || pluginFact.Selector != target.Selector {
		return transaction.UninstallResult{}, fmt.Errorf("installed marketplace/Plugin state conflicts with manifest: %w", transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
		Agent: agent, Power: ref, Adapter: pluginAdapter,
		Target: target, Marketplace: &marketplace, MarketplaceAdapter: marketplaceAdapter,
	})
}

// uninstallFilesystemPlugin 仅依据 manifest 与 Agent 本地事实卸载 filesystem Plugin，禁止访问 Plugin Resource 或 Cloud。
func (s *UninstallService) uninstallFilesystemPlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	actualName string,
	pluginAdapter adapter.PluginAdapter,
) (transaction.UninstallResult, error) {
	fact, err := pluginAdapter.Inspect(ctx, actualName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed filesystem Plugin %q: %w", actualName, err)
	}
	switch fact.Presence {
	case adapter.PluginAbsent:
		if fact.ActualName != actualName {
			return transaction.UninstallResult{}, fmt.Errorf("missing filesystem Plugin fact does not match manifest: %w", transaction.ErrIndeterminateState)
		}
		result := transaction.UninstallResult{TargetMissing: true}
		if err := s.config.Remove(agent, domain.PowerTypePlugin, ref.Name); err != nil {
			return result, fmt.Errorf("remove missing filesystem Plugin manifest entry: %w", err)
		}
		return result, nil
	case adapter.PluginPresent:
		if fact.ActualName != actualName || fact.Selector != actualName {
			return transaction.UninstallResult{}, fmt.Errorf("filesystem Plugin fact conflicts with manifest: %w", transaction.ErrIndeterminateState)
		}
	default:
		return transaction.UninstallResult{}, fmt.Errorf("unexpected filesystem Plugin presence %q: %w", fact.Presence, transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
		Agent: agent, Power: ref, Adapter: pluginAdapter,
		Target: adapter.PluginTarget{ActualName: actualName, Selector: actualName},
	})
}
