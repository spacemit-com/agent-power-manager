// Package logging 提供 APM 命令行的统一日志配置。
package logging

import (
	"io"
	"log/slog"
	"os"
)

func newTextHandler(writer io.Writer) slog.Handler {
	return slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	})
}

// NewLogger 使用指定 writer 和 verbose 配置创建分级日志器。
func NewLogger(stdout, stderr io.Writer, verbose bool) *slog.Logger {
	debug := &debugHandler{
		handler: newTextHandler(stderr),
		enabled: func() bool { return verbose },
	}
	return slog.New(&levelRouter{
		debug: debug,
		info:  newTextHandler(stdout),
		warn:  newTextHandler(stderr),
		error: newTextHandler(stderr),
	})
}

// SetupLogging 使用进程标准输出和标准错误安装默认日志器。
func SetupLogging(verbose bool) {
	slog.SetDefault(NewLogger(os.Stdout, os.Stderr, verbose))
}
