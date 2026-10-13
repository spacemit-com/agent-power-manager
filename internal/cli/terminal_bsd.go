//go:build darwin || freebsd

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminalFile 报告文件描述符是否连接到一个真正的终端；BSD 用 TIOCGETA 取终端属性。
// 不用 Mode&os.ModeCharDevice 的原因见 terminal_linux.go：/dev/null 也是字符设备。
func isTerminalFile(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}
