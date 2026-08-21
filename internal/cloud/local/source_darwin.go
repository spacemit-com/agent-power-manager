//go:build darwin

package local

import (
	"context"
	"io/fs"
)

func capturePhysicalTree(ctx context.Context, resourcesRoot, source string, beforeSnapshot func(), beforeFileOpen func(string), afterDataOpen func(string)) (*treeSnapshot, error) {
	return capturePhysicalTreeLinkSnapshot(ctx, resourcesRoot, source, beforeSnapshot, beforeFileOpen, afterDataOpen)
}

func capturePhysicalFile(ctx context.Context, resourcesRoot, source string, sizeLimit int64, beforeFileOpen func(string), afterDataOpen func(string)) ([]byte, fs.FileMode, error) {
	return capturePhysicalFileLinkSnapshot(ctx, resourcesRoot, source, sizeLimit, beforeFileOpen, afterDataOpen)
}
