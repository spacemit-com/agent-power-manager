//go:build !linux && !darwin && !freebsd && !windows

package cli

import "os"

// isTerminalFile 在未提供终端探测的平台上一律返回 false（保守：视为非交互）。
// upgrade 因此要求显式 --yes，而不是在无人可应答时静默继续。
func isTerminalFile(*os.File) bool { return false }
