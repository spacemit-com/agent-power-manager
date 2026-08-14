package cli

import "context"

// FixLifecycle 定义 fix 命令唯一需要的事务恢复入口。
type FixLifecycle interface {
	Fix(context.Context) error
}

// FixLifecycleFactory 在日志初始化后创建 Fix 生命周期。
type FixLifecycleFactory func() (FixLifecycle, error)
