package cli

import (
	"context"
	"fmt"

	"apm/internal/domain"
)

// InstallOutcome 表示单个 Agent 安装一个 Power 后的 CLI 可见结果。
type InstallOutcome string

const (
	// InstallOutcomeInstalled 表示首次安装成功。
	InstallOutcomeInstalled InstallOutcome = "installed"
	// InstallOutcomeUpgraded 表示从旧版本升级成功。
	InstallOutcomeUpgraded InstallOutcome = "upgraded"
	// InstallOutcomeDowngraded 表示从旧版本降级成功。
	InstallOutcomeDowngraded InstallOutcome = "downgraded"
	// InstallOutcomeUnchanged 表示目标版本已经满足请求。
	InstallOutcomeUnchanged InstallOutcome = "unchanged"
	// InstallOutcomeFailed 表示该 Agent 安装失败。
	InstallOutcomeFailed InstallOutcome = "failed"
	// InstallOutcomeUnexecuted 表示该 Agent 尚未执行安装。
	InstallOutcomeUnexecuted InstallOutcome = "unexecuted"
)

// InstallItem 表示一个 Agent 与 Power 的安装结果；CLI 按该结构逐行渲染。
type InstallItem struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Outcome InstallOutcome
}

// InstallResult 表示 CLI 可直接渲染的安装业务结果集合。
type InstallResult struct {
	Items []InstallItem
}

// InstallBatchError 表示批量安装中某个 Agent 失败及其底层原因。
type InstallBatchError struct {
	Agent domain.AgentID
	Err   error
}

func (e *InstallBatchError) Error() string {
	return fmt.Sprintf("install power for agent %q failed: %v", e.Agent, e.Err)
}

// Unwrap 返回当前失败 Agent 的底层原因。
func (e *InstallBatchError) Unwrap() error { return e.Err }

// InstallLifecycle 定义 install 命令唯一需要的生命周期入口。
type InstallLifecycle interface {
	Install(context.Context, Flags) (InstallResult, error)
}

// InstallLifecycleFactory 在日志初始化后创建 Install 生命周期。
type InstallLifecycleFactory func() (InstallLifecycle, error)
