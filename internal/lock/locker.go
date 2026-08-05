// Package lock 提供 APM 的进程间文件锁。
package lock

import "errors"

// ErrLocked 表示 APM 文件锁已由另一实例持有。
var ErrLocked = errors.New("APM lock is already held")

// Locker 表示可获取的进程间锁。
type Locker interface {
	// Acquire 非阻塞地获取锁，并返回独立的释放句柄。
	Acquire() (Unlock, error)
}

// Unlock 表示一次已成功获取的锁。
type Unlock interface {
	// Release 释放本句柄持有的锁。
	Release() error
}
