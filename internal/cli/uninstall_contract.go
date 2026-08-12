package cli

import (
	"context"
	"fmt"

	"apm/internal/domain"
)

// UninstallOutcome 表示单个 Agent 卸载一个 Power 后的 CLI 可见结果。
type UninstallOutcome string

const (
	// UninstallOutcomeCompleted 表示卸载成功完成。
	UninstallOutcomeCompleted UninstallOutcome = "completed"
	// UninstallOutcomeFailed 表示该 Agent 卸载失败。
	UninstallOutcomeFailed UninstallOutcome = "failed"
	// UninstallOutcomeUnexecuted 表示该 Agent 尚未执行卸载。
	UninstallOutcomeUnexecuted UninstallOutcome = "unexecuted"
)

// UninstallItem 表示一个 Agent 与可选 Power 的卸载结果；CLI 按该结构逐行渲染。
type UninstallItem struct {
	Agent   domain.AgentID
	Power   *domain.PowerRef
	Outcome UninstallOutcome
}

// UninstallResult 表示 CLI 可直接渲染的卸载业务结果集合。
type UninstallResult struct {
	Items []UninstallItem
}

// UninstallBatchError 表示批量卸载中某个 Agent 失败及其底层原因。
type UninstallBatchError struct {
	Agent domain.AgentID
	Err   error
}

func (e *UninstallBatchError) Error() string {
	return fmt.Sprintf("uninstall power for agent %q failed: %v", e.Agent, e.Err)
}

// Unwrap 返回当前失败 Agent 的底层原因。
func (e *UninstallBatchError) Unwrap() error { return e.Err }

// UninstallLifecycle 定义 uninstall 命令唯一需要的生命周期入口。
type UninstallLifecycle interface {
	Uninstall(context.Context, Flags) (UninstallResult, error)
}

// UninstallLifecycleFactory 在日志初始化后创建 Uninstall 生命周期。
type UninstallLifecycleFactory func() (UninstallLifecycle, error)
