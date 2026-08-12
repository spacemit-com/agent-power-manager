package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

// InstallService 实现 Install 命令在单个全局锁区间内的完整成功生命周期。
type InstallService struct {
	locker       Locker
	transactions TransactionManager
	cloud        CloudClient
	cache        PowerCache
	registry     AdapterRegistry
	config       ConfigRepository
	versions     VersionSelector
}

// NewInstall 创建持有完整共享状态区间的 Install 生命周期。
func NewInstall(locker Locker, transactions TransactionManager, cloud CloudClient, cache PowerCache, registry AdapterRegistry, config ConfigRepository, versions VersionSelector) (*InstallService, error) {
	switch {
	case isNilInterface(locker):
		return nil, errors.New("create Install lifecycle with nil locker")
	case isNilInterface(transactions):
		return nil, errors.New("create Install lifecycle with nil transaction manager")
	case isNilInterface(cloud):
		return nil, errors.New("create Install lifecycle with nil Cloud client")
	case isNilInterface(cache):
		return nil, errors.New("create Install lifecycle with nil power cache")
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
		cloud:        cloud,
		cache:        cache,
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

	refs, err := s.cloud.List()
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, fmt.Errorf("list Cloud powers for install: %w", err))
	}
	ref, err := resolveInstallPower(flags, refs, s.versions)
	if err != nil {
		return cli.InstallResult{}, releaseInstall(unlock, err)
	}

	items := make([]cli.InstallItem, 0, len(agents))
	var failedAgent domain.AgentID
	var businessErr error
	// 批处理首错即停止；后续 Agent 补为 unexecuted，使成功解锁后的汇总保持完整顺序。
	for index, agent := range agents {
		item, installErr := s.installOne(ctx, agent, ref)
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

func installAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	target := domain.AgentID(flags.Agent)
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
