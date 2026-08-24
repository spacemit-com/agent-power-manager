//go:build !linux && !darwin && !freebsd && !windows

package submission

import (
	"fmt"
	"os"
	"runtime"
)

func openPhysicalRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("physical regular files are unsupported on %s", runtime.GOOS)
	}
	return os.Open(path)
}

func sourceFileLinkCount(*os.File, os.FileInfo) (uint64, error) {
	return 0, fmt.Errorf("hard-link count is unavailable on %s", runtime.GOOS)
}
