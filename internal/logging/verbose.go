package logging

import (
	"context"
	"log/slog"
)

// Verbose 在启用 verbose 模式时将结构化诊断信息写入标准错误。
func (l *Logger) Verbose(message string, attrs ...slog.Attr) {
	if l.verbose {
		l.diagnostic.LogAttrs(context.Background(), slog.LevelDebug, message, attrs...)
	}
}
