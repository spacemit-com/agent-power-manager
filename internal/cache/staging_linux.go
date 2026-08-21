//go:build linux

package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// replaceStaging 在 Linux 首次发布时 rename，后续发布用 renameat2 exchange 保持 staging 根始终存在。
func replaceStaging(candidate, staging string) error {
	if _, err := os.Lstat(staging); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(candidate, staging)
	} else if err != nil {
		return fmt.Errorf("stat existing staging %q: %w", staging, err)
	}
	return unix.Renameat2(unix.AT_FDCWD, candidate, unix.AT_FDCWD, staging, unix.RENAME_EXCHANGE)
}
