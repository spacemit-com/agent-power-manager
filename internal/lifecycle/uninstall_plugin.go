package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallPlugin 只依据 manifest 指向的精确版本分类本地事实；所有分支均禁止连接 Cloud 或读取制品内容。
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

// uninstallMarketplacePlugin 以 Locate/Exists 分类目标状态后拒绝冲突，并将 absent/incomplete/complete 清理统一委托事务。
func (s *UninstallService) uninstallMarketplacePlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	actualName string,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (transaction.UninstallResult, error) {
	artifact, err := s.plugins.Locate(ref)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("locate installed Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	marketplace, err := artifact.MarketplaceFor(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve installed Plugin marketplace for agent %q: %w", agent, err)
	}
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, marketplace.Name)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed Plugin marketplace: %w", err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, actualName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed Plugin %q: %w", actualName, err)
	}
	// 卸载决策与缓存根无关：缓存根仅用于把“双事实齐备”拆分为 complete/incomplete，而两者都映射为
	// 非拒绝动作。因此这里不调用 Exists，避免 symlink/非目录/lstat 错误的损坏缓存根阻塞卸载
	//（spec §6.3/§7.2：损坏/缺失缓存不得阻塞卸载）。安装分类仍需 Exists 区分 complete/incomplete。
	facts := classifyMarketplacePluginTarget(actualName, marketplace, marketplaceFact, pluginFact, true)
	if decideUninstallState(true, facts.state).action == actionRejectState {
		return transaction.UninstallResult{}, fmt.Errorf("installed marketplace/Plugin state conflicts with manifest: %w", transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
		Agent: agent, Power: ref, Adapter: pluginAdapter,
		Target:             adapter.PluginTarget{ActualName: actualName, Selector: actualName},
		Marketplace:        &adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.Source},
		MarketplaceAdapter: marketplaceAdapter,
	})
}

// uninstallFilesystemPlugin 依据 Adapter 与物理路径事实分类，拒绝冲突后将清理统一委托事务，禁止访问 Plugin Resource 或 Cloud。
func (s *UninstallService) uninstallFilesystemPlugin(
	ctx context.Context,
	agent domain.AgentID,
	ref domain.PowerRef,
	actualName string,
	pluginAdapter adapter.PluginAdapter,
) (transaction.UninstallResult, error) {
	pluginFact, err := pluginAdapter.Inspect(ctx, actualName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("inspect installed filesystem Plugin %q: %w", actualName, err)
	}
	targetPath, err := pluginAdapter.TargetPath(actualName)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve installed filesystem Plugin target: %w", err)
	}
	physical, err := probeFilesystemPluginTarget(targetPath)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("validate installed filesystem Plugin target: %w", err)
	}
	facts := classifyFilesystemPluginTarget(actualName, pluginFact, physical)
	if decideUninstallState(true, facts.state).action == actionRejectState {
		return transaction.UninstallResult{}, fmt.Errorf("installed filesystem Plugin state conflicts with manifest: %w", transaction.ErrIndeterminateState)
	}
	return s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
		Agent: agent, Power: ref, Adapter: pluginAdapter,
		Target: adapter.PluginTarget{ActualName: actualName, Selector: actualName},
	})
}
