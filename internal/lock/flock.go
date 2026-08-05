package lock

import (
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/paths"

	"github.com/gofrs/flock"
)

type flockLocker struct {
	path string
}

type flockUnlock struct {
	file *flock.Flock
}

// New 创建使用指定文件路径的锁。
func New(path string) Locker {
	return &flockLocker{path: path}
}

// NewDefault 创建使用默认 APM 锁路径的锁。
func NewDefault() (Locker, error) {
	path, err := paths.LockPath()
	if err != nil {
		return nil, err
	}
	return New(path), nil
}

func (l *flockLocker) Acquire() (Unlock, error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	file := flock.New(l.path)
	acquired, err := file.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire APM lock: %w", err)
	}
	if !acquired {
		return nil, ErrLocked
	}
	return &flockUnlock{file: file}, nil
}

func (u *flockUnlock) Release() error {
	if err := u.file.Unlock(); err != nil {
		return fmt.Errorf("release APM lock: %w", err)
	}
	return nil
}
