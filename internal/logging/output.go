package logging

import (
	"fmt"
	"io"
	"os"
)

// NewDefault 使用进程的标准输出和标准错误创建 Logger。
func NewDefault(verbose bool) *Logger {
	return New(os.Stdout, os.Stderr, verbose)
}

func writeLine(writer io.Writer, message string) error {
	_, err := fmt.Fprintln(writer, message)
	return err
}

// Output 将面向用户的业务输出写入标准输出。
func (l *Logger) Output(value string) error {
	return writeLine(l.stdout, value)
}

// Warning 将警告信息写入标准错误。
func (l *Logger) Warning(value string) error {
	return writeLine(l.stderr, value)
}

// Error 将错误信息写入标准错误。
func (l *Logger) Error(value string) error {
	return writeLine(l.stderr, value)
}

// Recovery 将可恢复错误的信息写入标准错误。
func (l *Logger) Recovery(value string) error {
	return writeLine(l.stderr, value)
}
