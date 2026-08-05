// Package paths 定义 APM 本地文件的默认路径。
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

// LockPath 返回全局 APM 文件锁的默认路径。
func LockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return filepath.Join(home, ".apm", "apm.lock"), nil
}

// JournalPath 返回操作日志路径。
//
// 当前为零值占位实现。
func JournalPath() string {
	return ""
}

// ManifestPath 返回指定智能体的清单路径。
//
// 当前为零值占位实现。
func ManifestPath(domain.AgentID) string {
	return ""
}

// CachePath 返回缓存路径。
//
// 当前为零值占位实现。
func CachePath() string {
	return ""
}

// TransactionPath 返回事务记录路径。
//
// 当前为零值占位实现。
func TransactionPath() string {
	return ""
}
