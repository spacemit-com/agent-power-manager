//go:build !linux

package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// renameAgentNoReplace 在非 Linux 平台仅允许首次发布，避免以覆盖语义退化。
func renameAgentNoReplace(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fs.ErrExist
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check destination %q: %w", destination, err)
	}
	if err := os.Link(source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}
