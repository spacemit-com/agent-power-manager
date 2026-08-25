package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
)

// InstallService 实现 Install 命令在单个全局锁区间内的完整成功生命周期。
type InstallService struct {
	locker       lock.Locker
	transactions transaction.Manager
	catalogs     CatalogManager
	cloud        cloud.Client
	cache        cache.PowerCache
	plugins      pluginresource.Manager
	registry     AdapterRegistry
	config       config.Repository
	versions     VersionSelector
}

// NewInstall 创建持有完整共享状态区间的 Install 生命周期。
// plugins 仅允许对目标版本 Ensure；已安装旧版本必须在 manifest 判定后由 Lookup 离线读取。
func NewInstall(locker lock.Locker, transactions transaction.Manager, catalogs CatalogManager, cloud cloud.Client, cache cache.PowerCache, plugins pluginresource.Manager, registry AdapterRegistry, config config.Repository, versions VersionSelector) (*InstallService, error) {
	switch {
	case isNilInterface(locker):
		return nil, errors.New("create Install lifecycle with nil locker")
	case isNilInterface(transactions):
		return nil, errors.New("create Install lifecycle with nil transaction manager")
	case isNilInterface(catalogs):
		return nil, errors.New("create Install lifecycle with nil catalog manager")
	case isNilInterface(cloud):
		return nil, errors.New("create Install lifecycle with nil Cloud client")
	case isNilInterface(cache):
		return nil, errors.New("create Install lifecycle with nil power cache")
	case isNilInterface(plugins):
		return nil, errors.New("create Install lifecycle with nil Plugin Resource manager")
	case isNilInterface(registry):
		return nil, errors.New("create Install lifecycle with nil adapter registry")
	case isNilInterface(config):
		return nil, errors.New("create Install lifecycle with nil config repository")
	case isNilInterface(versions):
		return nil, errors.New("create Install lifecycle with nil version selector")
	}
	return &InstallService{
		locker:       locker,
		transactions: transactions,
		catalogs:     catalogs,
		cloud:        cloud,
		cache:        cache,
		plugins:      plugins,
		registry:     registry,
		config:       config,
		versions:     versions,
	}, nil
}

var _ cli.InstallLifecycle = (*InstallService)(nil)

// Install 在一次全局锁持有期间恢复共享状态并串行安装所选 Agent。
// 所有可渲染结果均在成功释放锁后返回，避免暴露仍受保护的中间状态。
func (s *InstallService) Install(ctx context.Context, flags cli.Flags) (cli.InstallResult, error) {
	// Recover、快照、Power 解析和逐 Agent Transaction 共同读写受保护状态，必须位于同一锁区间。
	unlock, err := s.locker.Acquire()
	if err != nil {
		return cli.InstallResult{}, fmt.Errorf("acquire install lock: %w", err)
	}

	recovery, err := s.transactions.Recover(ctx)
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, fmt.Errorf("recover transaction before install: %w", err))
	}
	logInstallRecovery(recovery)

	snapshot, err := s.registry.RefreshInstalled()
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, fmt.Errorf("refresh installed agents for install: %w", err))
	}
	agents, err := installAgents(flags, snapshot)
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, err)
	}
	if result, matched := s.explicitPluginManifestHit(flags, agents); matched {
		if releaseErr := unlock.Release(); releaseErr != nil {
			return cli.InstallResult{}, fmt.Errorf("release install lock: %w", releaseErr)
		}
		return result, nil
	}

	catalogSnapshot, catalogStatus, err := s.catalogs.SnapshotLocked(ctx)
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, fmt.Errorf("snapshot catalog for install: %w", err))
	}
	warnStaleCatalog("install", catalogStatus)
	entry, err := resolveCatalogEntry(flags, catalogSnapshot, s.versions)
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, err)
	}
	ref := entry.Ref

	items := make([]cli.InstallItem, 0, len(agents))
	var failedAgent domain.AgentID
	var businessErr error
	// 批处理首错即停止；后续 Agent 补为 unexecuted，使成功解锁后的汇总保持完整顺序。
	for index, agent := range agents {
		item, installErr := s.installForAgent(ctx, agent, entry, catalogSnapshot)
		if installErr != nil {
			failedAgent = agent
			businessErr = installErr
			items = append(items, cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeFailed})
			for _, skippedAgent := range agents[index+1:] {
				items = append(items, cli.InstallItem{Agent: skippedAgent, Power: ref, Outcome: cli.InstallOutcomeUnexecuted})
			}
			break
		}
		items = append(items, item)
	}

	// Release 失败时受保护状态不可确认，必须抑制全部可渲染结果并保留已发生的错误原因。
	if releaseErr := unlock.Release(); releaseErr != nil {
		return cli.InstallResult{}, errors.Join(
			businessErr,
			fmt.Errorf("release install lock: %w", releaseErr),
		)
	}
	result := cli.InstallResult{Items: items}
	if businessErr != nil {
		return result, &cli.InstallBatchError{Agent: failedAgent, Err: businessErr}
	}
	return result, nil
}

// explicitPluginManifestHit 仅处理类型、名称和规范版本都显式给出的 Plugin 批次。
// 任一 manifest 缺失、不匹配或不可读时回退既有 Cloud 解析路径，不在预检阶段改变错误优先级。
func (s *InstallService) explicitPluginManifestHit(flags cli.Flags, agents []domain.AgentID) (cli.InstallResult, bool) {
	if flags.Type != string(domain.PowerTypePlugin) || flags.Name == "" || flags.Version == "" {
		return cli.InstallResult{}, false
	}
	ref := domain.PowerRef{Name: flags.Name, Type: domain.PowerTypePlugin, Version: flags.Version}
	if _, err := domain.PluginRuntimeName(ref); err != nil {
		return cli.InstallResult{}, false
	}
	items := make([]cli.InstallItem, 0, len(agents))
	for _, agent := range agents {
		previous, installed, err := s.config.Find(agent, domain.PowerTypePlugin, ref.Name)
		if err != nil || !installed || previous != ref {
			return cli.InstallResult{}, false
		}
		items = append(items, pluginInstallItem(agent, ref, cli.InstallOutcomeUnchanged))
	}
	return cli.InstallResult{Items: items}, true
}

func installAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	if flags.Type == string(domain.PowerTypeAgent) && flags.Agent != "" && domain.AgentID(flags.Agent) != domain.AgentBibit {
		return nil, fmt.Errorf("install power type %q only supports agent %q", domain.PowerTypeAgent, domain.AgentBibit)
	}
	target := domain.AgentID(flags.Agent)
	if flags.Type == string(domain.PowerTypeAgent) && flags.Agent == "" {
		target = domain.AgentBibit
	}
	if target == domain.AllAgents {
		agents := snapshot.Ordered()
		if len(agents) == 0 {
			return nil, errors.New("install power: no supported installed agents")
		}
		return agents, nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("install power for agent %q: unsupported or not installed", target)
	}
	return []domain.AgentID{target}, nil
}

func releaseInstall(unlock lock.Unlock, businessErr error) error {
	if err := unlock.Release(); err != nil {
		return errors.Join(businessErr, fmt.Errorf("release install lock: %w", err))
	}
	return businessErr
}

func logInstallRecovery(result transaction.RecoveryResult) {
	if !result.Recovered {
		slog.Debug("no active transaction before install")
		return
	}
	slog.Warn(
		"transaction recovered",
		slog.String("operation", string(result.Operation)),
		logging.Agent(result.Agent),
		logging.Power(result.Power.Name),
		logging.Version(result.Power.Version),
		logging.Phase(string(result.OriginalPhase)),
		slog.String("direction", string(result.Direction)),
		slog.Any("actions", result.Actions),
	)
}
