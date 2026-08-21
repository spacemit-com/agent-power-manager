//go:build linux || darwin || freebsd

package local

import (
	"fmt"
	"io/fs"
	"syscall"
)

func fileLinkCount(info fs.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, fmt.Errorf("Unix file metadata does not expose Stat_t")
	}
	return uint64(stat.Nlink), nil
}
