// Package paths 定义 APM 本地文件的默认路径。
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

const (
	apmHomeEnv      = "APM_HOME"
	xdgCacheHomeEnv = "XDG_CACHE_HOME"
)

// HomeDir 返回 APM 使用的有效用户主目录。
func HomeDir() (string, error) {
	return resolveHome()
}

// StateDir 返回 APM 本地状态根目录。
func StateDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".apm"), nil
}

// LockPath 返回全局 APM 文件锁的默认路径。
func LockPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "apm.lock"), nil
}

// CatalogPath 返回全局唯一的持久化资源目录路径。
func CatalogPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "catalog.json"), nil
}

// JournalPath 返回唯一活动事务日志路径。
func JournalPath() (string, error) {
	root, err := TransactionPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "active.json"), nil
}

// ManifestPath 返回指定智能体的安装清单路径。
func ManifestPath(agent domain.AgentID) (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "manifests", string(agent)+".json"), nil
}

// CachePath 返回 APM 下载缓存目录。
func CachePath() (string, error) {
	if cache := os.Getenv(xdgCacheHomeEnv); cache != "" {
		return filepath.Join(cache, "apm"), nil
	}
	if home := os.Getenv(apmHomeEnv); home != "" {
		return filepath.Join(home, ".cache", "apm"), nil
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	if cache == "" {
		return "", errors.New("resolve user cache directory: empty path")
	}
	return filepath.Join(cache, "apm"), nil
}

// CacheTempPath 返回 Cloud 获取资源时使用的缓存临时目录。
func CacheTempPath() (string, error) {
	cache, err := CachePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, ".tmp"), nil
}

// StagingPath 返回当前 Power 的 staging 根目录。
func StagingPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "staging"), nil
}

// StagingTempPath 返回构建 staging 时使用的临时目录。
func StagingTempPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, ".tmp", "staging"), nil
}

// TransactionPath 返回集中事务状态根目录。
func TransactionPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "transactions"), nil
}

func resolveHome() (string, error) {
	if home := os.Getenv(apmHomeEnv); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return home, nil
}
