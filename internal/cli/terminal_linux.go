//go:build linux

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminalFile 报告文件描述符是否连接到一个真正的终端。
//
// 不能用 Mode&os.ModeCharDevice 判定：/dev/null 与 /dev/zero 也是字符设备，会把它误判成交互
// 终端，使 `apm upgrade < /dev/null` 与 CI 管道进入确认分支后永远读不到答复。这里改为询问
// 内核「能否取得终端属性」，语义与 isatty(3) 一致。
func isTerminalFile(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
