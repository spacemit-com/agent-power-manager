package lifecycle

import (
	"context"
	"log/slog"

	"apm/internal/logging"
	"apm/internal/transaction"
)

// inspectJournal 为只读命令检查活动事务；异常仅告警，避免阻断原本安全的查询流程。
func inspectJournal(ctx context.Context, command string, inspector transaction.Inspector) {
	result, err := inspector.Inspect(ctx)
	if err != nil {
		slog.Warn("transaction journal is damaged", slog.String("command", command), slog.Any("error", err))
		return
	}
	if !result.Active {
		slog.Debug("no active transaction before read-only command", slog.String("command", command))
		return
	}
	slog.Warn(
		"transaction recovery required",
		slog.String("command", command),
		slog.String("operation", string(result.Operation)),
		logging.Agent(result.Agent), logging.Power(result.Power.Name),
		logging.Version(result.Power.Version),
		logging.Phase(string(result.OriginalPhase)),
		slog.String("hint", "run apm fix"),
	)
}
