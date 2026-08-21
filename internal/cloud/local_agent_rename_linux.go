//go:build linux

package cloud

import "golang.org/x/sys/unix"

// renameAgentNoReplace 在 Linux 通过 renameat2 保证并发创建 destination 时绝不覆盖已有文件。
func renameAgentNoReplace(source, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}
