//go:build darwin

package local

import (
	"context"
	"io/fs"
)

func capturePhysicalTree(ctx context.Context, resourcesRoot, source string, beforeSnapshot func(), beforeFileOpen func(string), afterDataOpen func(string)) (*treeSnapshot, error) {
	return capturePhysicalTreeLinkSnapshotWithPolicy(ctx, resourcesRoot, source, defaultLocalTreeContentPolicy, beforeSnapshot, beforeFileOpen, afterDataOpen)
}

// capturePhysicalTreeWithPolicy 使用调用方内容策略固定物理树快照；
// policy 为 nil 时退化为默认文本-only 策略。
func capturePhysicalTreeWithPolicy(ctx context.Context, resourcesRoot, source string, policy localTreeContentPolicy, beforeSnapshot func(), beforeFileOpen func(string), afterDataOpen func(string)) (*treeSnapshot, error) {
	return capturePhysicalTreeLinkSnapshotWithPolicy(ctx, resourcesRoot, source, policy, beforeSnapshot, beforeFileOpen, afterDataOpen)
}

func capturePhysicalFile(ctx context.Context, resourcesRoot, source string, sizeLimit int64, beforeFileOpen func(string), afterDataOpen func(string)) ([]byte, fs.FileMode, error) {
	return capturePhysicalFileLinkSnapshot(ctx, resourcesRoot, source, sizeLimit, beforeFileOpen, afterDataOpen)
}
