package cli

import (
	"context"
)

// SearchResult 表示 CLI 可直接渲染的搜索业务结果。
type SearchResult struct {
	Powers []ListedPower
}

// SearchLifecycle 定义 search 命令唯一需要的生命周期入口。
type SearchLifecycle interface {
	Search(context.Context, Flags) (SearchResult, error)
}

// SearchLifecycleFactory 在日志初始化后创建 Search 生命周期。
type SearchLifecycleFactory func() (SearchLifecycle, error)
