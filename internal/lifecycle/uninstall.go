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

// UninstallService 实现 Uninstall 命令在单个全局锁区间内的本地卸载生命周期。
type UninstallService struct {
	locker       Locker
	transactions TransactionManager
	registry     AdapterRegistry
	config       ConfigRepository
}

// NewUninstall 创建持有完整共享状态区间的 Uninstall 生命周期。
func NewUninstall(locker Locker, transactions TransactionManager, registry AdapterRegistry, config ConfigRepository) (*UninstallService, error) {
	switch {
	case isNilInterface(locker):
		return nil, errors.New("create Uninstall lifecycle with nil locker")
	case isNilInterface(transactions):
		return nil, errors.New("create Uninstall lifecycle with nil transaction manager")
	case isNilInterface(registry):
		return nil, errors.New("create Uninstall lifecycle with nil adapter registry")
	case isNilInterface(config):
		return nil, errors.New("create Uninstall lifecycle with nil config repository")
	}
	return &UninstallService{locker: locker, transactions: transactions, registry: registry, config: config}, nil
}

var _ cli.UninstallLifecycle = (*UninstallService)(nil)

// Uninstall 在一次全局锁持有期间恢复共享状态，并按快照顺序串行卸载所选 Agent。
// 可渲染结果仅在锁成功释放后返回；业务 Warn 则在相关事实发生时同步写出。
func (s *UninstallService) Uninstall(ctx context.Context, flags cli.Flags) (cli.UninstallResult, error) {
	unlock, err := s.locker.Acquire()
	if err != nil {
		return cli.UninstallResult{}, fmt.Errorf("acquire uninstall lock: %w", err)
	}
	recovery, err := s.transactions.Recover(ctx)
	if err != nil {
		return cli.UninstallResult{}, releaseUninstall(unlock, fmt.Errorf("recover transaction before uninstall: %w", err))
	}
	logUninstallRecovery(recovery)

	snapshot, err := s.registry.RefreshInstalled()
	if err != nil {
		return cli.UninstallResult{}, releaseUninstall(unlock, fmt.Errorf("refresh installed agents for uninstall: %w", err))
	}
	agents, err := uninstallAgents(flags, snapshot)
	if err != nil {
		return cli.UninstallResult{}, releaseUninstall(unlock, err)
	}

	items := make([]cli.UninstallItem, 0, len(agents))
	var failedAgent domain.AgentID
	var businessErr error
	for index, agent := range agents {
		item, itemErr := s.uninstallOne(ctx, agent, flags)
		items = append(items, item)
		if itemErr == nil {
			continue
		}
		failedAgent, businessErr = agent, itemErr
		for _, later := range agents[index+1:] {
			items = append(items, cli.UninstallItem{Agent: later, Outcome: cli.UninstallOutcomeUnexecuted})
		}
		break
	}

	if releaseErr := unlock.Release(); releaseErr != nil {
		return cli.UninstallResult{}, errors.Join(businessErr, fmt.Errorf("release uninstall lock: %w", releaseErr))
	}
	result := cli.UninstallResult{Items: items}
	if businessErr != nil {
		return result, &cli.UninstallBatchError{Agent: failedAgent, Err: businessErr}
	}
	return result, nil
}

func uninstallAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	target := domain.AgentID(flags.Agent)
	if target == domain.AllAgents {
		agents := snapshot.Ordered()
		if len(agents) == 0 {
			return nil, errors.New("uninstall power: no supported installed agents")
		}
		return agents, nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("uninstall power for agent %q: unsupported or not installed", target)
	}
	return []domain.AgentID{target}, nil
}

func releaseUninstall(unlock lock.Unlock, businessErr error) error {
	if err := unlock.Release(); err != nil {
		return errors.Join(businessErr, fmt.Errorf("release uninstall lock: %w", err))
	}
	return businessErr
}

func logUninstallRecovery(result transaction.RecoveryResult) {
	if !result.Recovered {
		slog.Debug("no active transaction before uninstall")
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
