// Package logging 提供 APM 命令行的统一日志输出。
package logging

import (
	"io"
	"log/slog"
)

// Logger 将业务输出与诊断输出路由到各自的 writer。
type Logger struct {
	stdout     io.Writer
	stderr     io.Writer
	verbose    bool
	diagnostic *slog.Logger
}

// New 使用指定的标准输出、标准错误和 verbose 开关创建 Logger。
func New(stdout, stderr io.Writer, verbose bool) *Logger {
	return &Logger{
		stdout:  stdout,
		stderr:  stderr,
		verbose: verbose,
		diagnostic: slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})),
	}
}

// SetVerbose 设置是否输出 verbose 诊断日志。
func (l *Logger) SetVerbose(verbose bool) {
	l.verbose = verbose
}

// Stdout 返回 Logger 使用的标准输出 writer。
func (l *Logger) Stdout() io.Writer {
	return l.stdout
}

// Stderr 返回 Logger 使用的标准错误 writer。
func (l *Logger) Stderr() io.Writer {
	return l.stderr
}
