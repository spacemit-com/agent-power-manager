//go:build !linux

package local

import (
	"context"
	"io/fs"
	"runtime"
)

func capturePhysicalTree(context.Context, string, string, func(), func(string)) (*treeSnapshot, error) {
	return nil, unsafeContent("descriptor-safe Local tree validation is unsupported on %s", runtime.GOOS)
}

func capturePhysicalFile(context.Context, string, string, int64, func(string)) ([]byte, fs.FileMode, error) {
	return nil, 0, unsafeContent("descriptor-safe Local file validation is unsupported on %s", runtime.GOOS)
}
