//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// isTerminalFile 报告文件描述符是否连接到 Windows 控制台。
// 不用 Mode&os.ModeCharDevice 的原因与 Unix 侧相同：字符设备不等于交互终端。
func isTerminalFile(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}
