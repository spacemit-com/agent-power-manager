//go:build !linux && !darwin && !freebsd && !windows

package pluginartifact

import (
	"fmt"
	"io/fs"
	"runtime"
)

func physicalFileLinkCount(string, fs.FileInfo) (uint64, error) {
	return 0, fmt.Errorf("hard-link count is unavailable for Plugin artifact validation on %s", runtime.GOOS)
}
