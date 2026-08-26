package agentartifact

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

const (
	// maxFileBytes 是单个普通文件的最大字节数（含）。
	maxFileBytes = 10 << 20
	// maxTotalBytes 是全部普通文件的总字节上限（含）。
	maxTotalBytes = 100 << 20
	// maxNodeCount 是目录树（文件 + 目录）的节点数上限（含）。
	maxNodeCount = 1000
	// maxDepth 是规范相对路径的最大深度（路径段数，含）。
	maxDepth = 16
	// maxRelativePathBytes 是规范相对路径的最大字节数（含）。
	maxRelativePathBytes = 255
)

// ValidateDirectory 校验物理 Agent 目录：先执行 pluginartifact.ValidatePhysicalTree
// 的物理树检查（拒绝非目录根、symlink、硬链接与特殊文件），再按公共阈值
// 读取并转换为逻辑快照，最后调用 Validate。ctx 取消时中止遍历并返回取消原因。
// 20 MiB 压缩上限与 100x 膨胀率属于归档下载边界，不在本函数内检查。
func ValidateDirectory(ctx context.Context, root string, ref *domain.PowerRef) (Snapshot, error) {
	if err := pluginartifact.ValidatePhysicalTree(root); err != nil {
		return Snapshot{}, err
	}
	entries := make([]Entry, 0)
	var totalBytes int64
	nodeCount := 0
	err := filepath.WalkDir(root, func(current string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return agentError("compute relative path for %q: %v", current, err)
		}
		if relative == "." {
			return nil
		}
		portable := filepath.ToSlash(relative)
		nodeCount++
		if nodeCount > maxNodeCount {
			return agentError("agent artifact exceeds %d nodes", maxNodeCount)
		}
		if len([]byte(portable)) > maxRelativePathBytes {
			return agentError("agent artifact path %q exceeds %d bytes", portable, maxRelativePathBytes)
		}
		if strings.Count(portable, "/")+1 > maxDepth {
			return agentError("agent artifact path %q exceeds depth %d", portable, maxDepth)
		}
		info, err := dirEntry.Info()
		if err != nil {
			return agentError("lstat agent artifact path %q: %v", portable, err)
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || !dirEntry.IsDir() && !mode.IsRegular() {
			return agentError("agent artifact path %q is not a regular file or directory", portable)
		}
		entry := Entry{Path: portable, Directory: dirEntry.IsDir(), Mode: mode}
		if !entry.Directory {
			if info.Size() > maxFileBytes {
				return agentError("agent artifact file %q exceeds %d bytes", portable, maxFileBytes)
			}
			data, err := readBoundedFile(current, maxFileBytes)
			if err != nil {
				return agentError("read agent artifact file %q: %v", portable, err)
			}
			totalBytes += int64(len(data))
			if totalBytes > maxTotalBytes {
				return agentError("agent artifact content exceeds %d bytes total", maxTotalBytes)
			}
			entry.Data = data
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return Validate(entries, ref)
}

// readBoundedFile 读取普通文件；正文超过 limit 字节时拒绝，不把超限文件读入内存。
func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file exceeds byte limit")
	}
	return data, nil
}
