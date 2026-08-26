//go:build !linux && !darwin && !freebsd

package pluginartifact

import (
	"fmt"
	"os"
	"runtime"
)

func openChildFd(directory *os.File, name string) (*os.File, error) {
	return nil, fmt.Errorf("fd-relative Plugin artifact validation is unsupported on %s", runtime.GOOS)
}
