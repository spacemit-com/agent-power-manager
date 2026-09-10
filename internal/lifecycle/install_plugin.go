package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/pluginresource"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// installPlugin 先核对本地清单与目标 Agent 双事实，按共享状态策略决策后再准备资源并交由 Plugin 事务发布。
func (s *InstallService) installPlugin(ctx context.Context, agent domain.AgentID, entry domain.CatalogEntry) (cli.InstallItem, error) {
	ref := entry.Ref
	previous, installed, err := s.config.Find(agent, ref.Type, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed Plugin %+v for agent %q: %w", ref, agent, err)
	}
	manifest := manifestAbsent
	if installed && previous.Ref.Version == ref.Version {
		manifest = manifestSameVersion
	} else if installed {
		manifest = manifestDifferentVersion
	}
	if manifest == manifestSameVersion {
		return pluginInstallItem(agent, ref, cli.InstallOutcomeUnchanged), nil
	}
	runtimeName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive Plugin runtime name: %w", err)
	}

	pluginAdapter, err := s.registry.ResolvePlugin(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve Plugin adapter for agent %q: %w", agent, err)
	}

	// marketplace 部署使用完整 selector（<name>@apm-<name>），filesystem 部署沿用裸实际名。
	selector := runtimeName
	if pluginAdapter.DeploymentMode() == domain.PluginDeploymentMarketplace {
		if selector, err = domain.PluginSelector(ref); err != nil {
			return cli.InstallItem{}, fmt.Errorf("derive Plugin selector: %w", err)
		}
	}
	target := adapter.PluginTarget{ActualName: runtimeName, Selector: selector}

	switch pluginAdapter.DeploymentMode() {
	case domain.PluginDeploymentMarketplace:
		marketplaceAdapter, ok := pluginAdapter.(adapter.MarketplacePluginAdapter)
		if !ok {
			return cli.InstallItem{}, fmt.Errorf("resolve marketplace Plugin adapter for agent %q: %w", agent, adapter.ErrUnsupportedAdapter)
		}
		return s.installMarketplacePlugin(ctx, agent, entry, ref, previous.Ref, manifest, target, pluginAdapter, marketplaceAdapter)
	case domain.PluginDeploymentFilesystem:
		return s.installFilesystemPlugin(ctx, agent, entry, ref, previous.Ref, manifest, target, pluginAdapter)
	default:
		return cli.InstallItem{}, fmt.Errorf("unsupported Plugin deployment %q: %w", pluginAdapter.DeploymentMode(), adapter.ErrUnsupportedAdapter)
	}
}

func (s *InstallService) installMarketplacePlugin(
	ctx context.Context,
	agent domain.AgentID,
	entry domain.CatalogEntry,
	ref domain.PowerRef,
	previous domain.PowerRef,
	manifest manifestState,
	target adapter.PluginTarget,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	// 版本切换分类旧版本状态；全新安装分类目标版本状态。
	classifyRef := ref
	if manifest == manifestDifferentVersion {
		classifyRef = previous
	}
	artifact, err := s.plugins.Locate(classifyRef)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("locate Plugin artifact %+v for agent %q: %w", classifyRef, agent, err)
	}
	marketplace, err := artifact.MarketplaceFor(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve Plugin marketplace for agent %q: %w", agent, err)
	}
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, marketplace.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect Plugin marketplace for agent %q: %w", agent, err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, target.ActualName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect Plugin %q for agent %q: %w", target.ActualName, agent, err)
	}
	cacheExists, err := s.plugins.Exists(classifyRef)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect Plugin artifact cache %+v for agent %q: %w", classifyRef, agent, err)
	}
	facts := classifyMarketplacePluginTarget(target.ActualName, marketplace, marketplaceFact, pluginFact, cacheExists)

	switch decideInstallState(manifest, facts.state).action {
	case actionInstallNew:
		return s.installMarketplacePluginFresh(ctx, agent, entry, ref, target, pluginAdapter, marketplaceAdapter)
	case actionReplace:
		return s.installMarketplacePluginReplace(ctx, agent, entry, ref, previous, target, pluginAdapter, marketplaceAdapter)
	case actionCleanAndInstall:
		logInstallMissingTarget(agent, previous)
		previousName, err := domain.PluginRuntimeName(previous)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("derive previous Plugin actual name: %w", err)
		}
		previousSelector, err := domain.PluginSelector(previous)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("derive previous Plugin selector: %w", err)
		}
		if _, err := s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
			Agent: agent, Power: previous, Adapter: pluginAdapter,
			Target:             adapter.PluginTarget{ActualName: previousName, Selector: previousSelector},
			Marketplace:        &adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.Source},
			MarketplaceAdapter: marketplaceAdapter,
		}); err != nil {
			return cli.InstallItem{}, fmt.Errorf("repair installed Plugin %+v for agent %q: %w", previous, agent, err)
		}
		return s.installMarketplacePluginFresh(ctx, agent, entry, ref, target, pluginAdapter, marketplaceAdapter)
	case actionRejectConflict:
		return cli.InstallItem{}, fmt.Errorf("unmanaged Plugin %q for agent %q already exists: %w", ref.Name, agent, adapter.ErrTargetConflict)
	default:
		return cli.InstallItem{}, fmt.Errorf("installed Plugin %+v for agent %q conflicts with manifest: %w", previous, agent, transaction.ErrIndeterminateState)
	}
}

// installMarketplacePluginFresh 以全新安装语义发布目标版本：Ensure 目标制品并交由事务执行联合首装。
func (s *InstallService) installMarketplacePluginFresh(
	ctx context.Context,
	agent domain.AgentID,
	entry domain.CatalogEntry,
	ref domain.PowerRef,
	target adapter.PluginTarget,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	targetArtifact, err := s.plugins.Ensure(ctx, entry)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("ensure target Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	if err := validateLifecyclePluginArtifact(targetArtifact, ref); err != nil {
		return cli.InstallItem{}, err
	}
	targetMarketplace, err := targetArtifact.MarketplaceFor(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve target Plugin marketplace for agent %q: %w", agent, err)
	}
	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Description: entry.Description, Tags: entry.Tags, Adapter: pluginAdapter,
		Target: target,
		Marketplace: &transaction.PluginMarketplaceInstallRequest{
			Target: marketplaceTarget(targetMarketplace), Adapter: marketplaceAdapter,
		},
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	return pluginInstallItem(agent, ref, cli.InstallOutcomeInstalled), nil
}

// installMarketplacePluginReplace 保持旧版本离线 Lookup 复验后执行原子版本切换。
func (s *InstallService) installMarketplacePluginReplace(
	ctx context.Context,
	agent domain.AgentID,
	entry domain.CatalogEntry,
	ref domain.PowerRef,
	previous domain.PowerRef,
	target adapter.PluginTarget,
	pluginAdapter adapter.PluginAdapter,
	marketplaceAdapter adapter.MarketplacePluginAdapter,
) (cli.InstallItem, error) {
	targetArtifact, err := s.plugins.Ensure(ctx, entry)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("ensure target Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	if err := validateLifecyclePluginArtifact(targetArtifact, ref); err != nil {
		return cli.InstallItem{}, err
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
	targetMarketplaceResource, err := targetArtifact.MarketplaceFor(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve target Plugin marketplace for agent %q: %w", agent, err)
	}
	previousName, err := domain.PluginRuntimeName(previous)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive previous Plugin actual name: %w", err)
	}
	previousSelector, err := domain.PluginSelector(previous)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive previous Plugin selector: %w", err)
	}
	previousTarget := adapter.PluginTarget{ActualName: previousName, Selector: previousSelector}
	previousMarketplace := marketplaceTarget(previousMarketplaceResource)
	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Description: entry.Description, Tags: entry.Tags, Previous: powerRefPointer(previous), Adapter: pluginAdapter,
		Target:         target,
		PreviousTarget: &previousTarget,
		Marketplace: &transaction.PluginMarketplaceInstallRequest{
			Target: marketplaceTarget(targetMarketplaceResource), Previous: &previousMarketplace, Adapter: marketplaceAdapter,
		},
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	return pluginInstallItem(agent, ref, pluginVersionOutcome(previous, ref)), nil
}

// installFilesystemPlugin 对任意 filesystem Agent 复用同一逻辑目标；staging 只能在本地事实校验通过后生成。
func (s *InstallService) installFilesystemPlugin(
	ctx context.Context,
	agent domain.AgentID,
	entry domain.CatalogEntry,
	ref domain.PowerRef,
	previous domain.PowerRef,
	manifest manifestState,
	target adapter.PluginTarget,
	pluginAdapter adapter.PluginAdapter,
) (cli.InstallItem, error) {
	classifyRef := ref
	if manifest == manifestDifferentVersion {
		classifyRef = previous
	}
	classifyName, err := domain.PluginRuntimeName(classifyRef)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("derive filesystem Plugin name: %w", err)
	}
	pluginFact, err := pluginAdapter.Inspect(ctx, classifyName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect filesystem Plugin %q for agent %q: %w", classifyName, agent, err)
	}
	targetPath, err := pluginAdapter.TargetPath(classifyName)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve filesystem Plugin target for agent %q: %w", agent, err)
	}
	physical, err := probeFilesystemPluginTarget(targetPath)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("validate filesystem Plugin target for agent %q: %w", agent, err)
	}
	facts := classifyFilesystemPluginTarget(classifyName, pluginFact, physical)

	switch decideInstallState(manifest, facts.state).action {
	case actionInstallNew:
		return s.installFilesystemPluginPublish(ctx, agent, entry, ref, nil, target, pluginAdapter)
	case actionReplace:
		return s.installFilesystemPluginPublish(ctx, agent, entry, ref, powerRefPointer(previous), target, pluginAdapter)
	case actionCleanAndInstall:
		logInstallMissingTarget(agent, previous)
		if _, err := s.transactions.UninstallPlugin(ctx, transaction.PluginUninstallRequest{
			Agent: agent, Power: previous, Adapter: pluginAdapter,
			Target: adapter.PluginTarget{ActualName: classifyName, Selector: classifyName},
		}); err != nil {
			return cli.InstallItem{}, fmt.Errorf("repair installed filesystem Plugin %+v for agent %q: %w", previous, agent, err)
		}
		return s.installFilesystemPluginPublish(ctx, agent, entry, ref, nil, target, pluginAdapter)
	case actionRejectConflict:
		return cli.InstallItem{}, fmt.Errorf("unmanaged filesystem Plugin %q for agent %q already exists: %w", ref.Name, agent, adapter.ErrTargetConflict)
	default:
		return cli.InstallItem{}, fmt.Errorf("installed filesystem Plugin %+v for agent %q conflicts with manifest: %w", previous, agent, transaction.ErrIndeterminateState)
	}
}

// installFilesystemPluginPublish 在事实校验通过后 Ensure/Stage 并发布；previous 非空时执行原子版本切换。
func (s *InstallService) installFilesystemPluginPublish(
	ctx context.Context,
	agent domain.AgentID,
	entry domain.CatalogEntry,
	ref domain.PowerRef,
	previous *domain.PowerRef,
	target adapter.PluginTarget,
	pluginAdapter adapter.PluginAdapter,
) (cli.InstallItem, error) {
	targetArtifact, err := s.plugins.Ensure(ctx, entry)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("ensure target Plugin artifact %+v for agent %q: %w", ref, agent, err)
	}
	if err := validateLifecyclePluginArtifact(targetArtifact, ref); err != nil {
		return cli.InstallItem{}, err
	}
	// 通用 filesystem staging 仅在所有事实校验完成、即将进入发布事务时调用。
	staging, err := s.plugins.StageFilesystem(ref)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("stage filesystem Plugin artifact %+v: %w", ref, err)
	}
	request := transaction.PluginInstallRequest{
		Agent: agent, Power: ref, Description: entry.Description, Tags: entry.Tags, StagingRoot: staging, Adapter: pluginAdapter,
		Target: target,
	}
	if previous != nil {
		request.Previous = previous
	}
	if err := s.transactions.InstallPlugin(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install Plugin %+v for agent %q: %w", ref, agent, err)
	}
	outcome := cli.InstallOutcomeInstalled
	if previous != nil {
		outcome = pluginVersionOutcome(*previous, ref)
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
