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

// agentPowerGroups 保存单个 Agent 的已安装能力分组。
type agentPowerGroups struct {
	agent  domain.AgentID
	groups domain.PowerGroups
}

func installedListAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	target := domain.AgentID(flags.Agent)
	if target == domain.AllAgents {
		return snapshot.Ordered(), nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("list installed powers for agent %q: unsupported or not installed", target)
	}
	return []domain.AgentID{target}, nil
}

func releaseInstalledList(unlock lock.Unlock, businessErr error) error {
	if err := unlock.Release(); err != nil {
		return errors.Join(businessErr, fmt.Errorf("release installed list lock: %w", err))
	}
	return businessErr
}

// listInstalled 的 Recover、快照刷新和全部 Config 读取共享同一锁区间；恢复日志也在锁内记录。
// 结果转换等待成功 Release 后才执行。
func (s *ListService) listInstalled(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
	unlock, err := s.locker.Acquire()
	if err != nil {
		return cli.ListResult{}, fmt.Errorf("acquire installed list lock: %w", err)
	}

	recovery, err := s.transactions.Recover(ctx)
	if err != nil {
		return cli.ListResult{}, releaseInstalledList(
			unlock,
			fmt.Errorf("recover transaction before installed list: %w", err),
		)
	}
	logInstalledListRecovery(recovery)

	snapshot, err := s.registry.RefreshInstalled()
	if err != nil {
		return cli.ListResult{}, releaseInstalledList(
			unlock,
			fmt.Errorf("refresh installed agents for list: %w", err),
		)
	}
	agents, err := installedListAgents(flags, snapshot)
	if err != nil {
		return cli.ListResult{}, releaseInstalledList(unlock, err)
	}

	inputs := make([]agentPowerGroups, 0, len(agents))
	failures := make([]cli.AgentFailure, 0)
	for _, agent := range agents {
		groups, listErr := s.config.List(agent)
		if listErr != nil {
			wrapped := fmt.Errorf("list installed powers for agent %q: %w", agent, listErr)
			if domain.AgentID(flags.Agent) != domain.AllAgents {
				return cli.ListResult{}, releaseInstalledList(unlock, wrapped)
			}
			failures = append(failures, cli.AgentFailure{Agent: agent, Err: wrapped})
			continue
		}
		inputs = append(inputs, agentPowerGroups{agent: agent, groups: groups})
	}

	if releaseErr := unlock.Release(); releaseErr != nil {
		causes := []error{fmt.Errorf("release installed list lock: %w", releaseErr)}
		for _, failure := range failures {
			causes = append(causes, failure.Err)
		}
		return cli.ListResult{}, errors.Join(causes...)
	}

	result := makeInstalledListResult(flags, inputs)
	if len(failures) != 0 {
		return result, &cli.PartialListError{Failures: failures}
	}
	return result, nil
}

func logInstalledListRecovery(result transaction.RecoveryResult) {
	if !result.Recovered {
		slog.Debug("no active transaction before installed list")
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
