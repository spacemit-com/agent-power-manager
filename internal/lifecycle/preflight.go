package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"apm/internal/logging"
)

func (s *Service) recoverSearch(ctx context.Context) error {
	unlock, err := s.locker.Acquire()
	if err != nil {
		return fmt.Errorf("acquire search recovery lock: %w", err)
	}

	// 恢复会修改跨命令共享状态，必须在持有全局锁期间执行。
	result, recoveryErr := s.transactions.Recover(ctx)
	if recoveryErr != nil {
		releaseErr := unlock.Release()
		if releaseErr != nil {
			return errors.Join(
				fmt.Errorf("recover transaction before search: %w", recoveryErr),
				fmt.Errorf("release search recovery lock: %w", releaseErr),
			)
		}
		return fmt.Errorf("recover transaction before search: %w", recoveryErr)
	}
	if result.Recovered {
		// 成功恢复的 Warn 必须同步写入，并且仍处于全局锁保护范围内。
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
	} else {
		slog.Debug("no active transaction before search")
	}
	if err := unlock.Release(); err != nil {
		return fmt.Errorf("release search recovery lock: %w", err)
	}
	return nil
}
