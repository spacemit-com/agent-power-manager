//go:build !linux && !darwin && !freebsd && !windows

package local

import (
	"fmt"
	"io/fs"
	"runtime"
)

func fileLinkCount(fs.FileInfo) (uint64, error) {
	return 0, fmt.Errorf("hard-link count is unavailable for Local source validation on %s", runtime.GOOS)
}
