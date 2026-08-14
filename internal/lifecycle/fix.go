package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

// FixService 在一次全局锁持有期间收敛一个遗留事务。
type FixService struct {
	locker    lock.Locker
	recoverer transaction.Recoverer
}

// NewFix 创建只依赖锁和事务恢复器的单次修复生命周期。
func NewFix(locker lock.Locker, recoverer transaction.Recoverer) (*FixService, error) {
	switch {
	case isNilInterface(locker):
		return nil, errors.New("create Fix lifecycle with nil locker")
	case isNilInterface(recoverer):
		return nil, errors.New("create Fix lifecycle with nil transaction recoverer")
	}
	return &FixService{locker: locker, recoverer: recoverer}, nil
}

// Fix 在锁内恰好执行一次恢复，并在返回前同步完成日志和解锁。
func (s *FixService) Fix(ctx context.Context) error {
	unlock, err := s.locker.Acquire()
	if err != nil {
		return fmt.Errorf("acquire fix lock: %w", err)
	}

	result, recoverErr := s.recoverer.Recover(ctx)
	if recoverErr == nil {
		logFixRecovery(result)
	}
	if releaseErr := unlock.Release(); releaseErr != nil {
		return errors.Join(
			wrapFixRecoveryError(recoverErr),
			fmt.Errorf("release fix lock: %w", releaseErr),
		)
	}
	return wrapFixRecoveryError(recoverErr)
}

func wrapFixRecoveryError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("recover transaction for fix: %w", err)
}

// logFixRecovery 将恢复事实写入日志；无遗留事务时仅记录 Debug 诊断。
func logFixRecovery(result transaction.RecoveryResult) {
	if !result.Recovered {
		slog.Debug("no active transaction before fix")
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
