//go:build !linux

package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// replaceStaging 在非 Linux 平台拒绝替换已有 staging，以优先保留完整旧根目录。
func replaceStaging(candidate, staging string) error {
	if _, err := os.Lstat(staging); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(candidate, staging)
	} else if err != nil {
		return fmt.Errorf("stat existing staging %q: %w", staging, err)
	}
	return errors.New("atomic staging replacement requires Linux renameat2")
}
