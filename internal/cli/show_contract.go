package cli

import "context"

// ShowResult 表示 show 命令返回的单个 Cloud Power 详情。
type ShowResult struct {
	Power ListedPower
}

// ShowLifecycle 定义 show 命令唯一需要的生命周期入口。
type ShowLifecycle interface {
	Show(context.Context, Flags) (ShowResult, error)
}

// ShowLifecycleFactory 在日志初始化后创建 Show 生命周期。
type ShowLifecycleFactory func() (ShowLifecycle, error)
