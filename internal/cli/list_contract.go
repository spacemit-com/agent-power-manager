package cli

import (
	"context"
	"fmt"
	"strings"

	"apm/internal/domain"
)

// ListedPower 表示 List 可渲染的一条 Power；Cloud 记录的 Agent 为空，已安装记录的 Agent 必填。
// Description 与 Tags 仅在 --all 时参与文本或 JSON 输出。
type ListedPower struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Description string
	Tags        []string
}

// ListResult 表示 CLI 可直接渲染的 List 业务结果。
type ListResult struct {
	Powers []ListedPower
	// Agents 仅在 installed 的 all 模式（省略 --agent 或 --agent=all）下按 Agent 顺序汇总；
	// Cloud 模式与具体 Agent 模式为 nil。
	Agents []InstalledAgentSummary
}

// InstalledAgentState 表示 installed 列表中单个 Agent 的清单状态。
type InstalledAgentState string

const (
	// InstalledAgentNotInstalled 表示 Agent profile 根不存在。
	InstalledAgentNotInstalled InstalledAgentState = "not-installed"
	// InstalledAgentEmpty 表示 Agent 已安装，但过滤后无任何能力可输出。
	InstalledAgentEmpty InstalledAgentState = "empty"
	// InstalledAgentListed 表示 Agent 过滤后至少有一条能力。
	InstalledAgentListed InstalledAgentState = "listed"
)

// InstalledAgentSummary 保存 installed all 模式中单个 Agent 的清单状态。
type InstalledAgentSummary struct {
	Agent domain.AgentID
	State InstalledAgentState
}

// AgentFailure 保存 all 模式中一个 Agent 的清单读取失败。
type AgentFailure struct {
	Agent domain.AgentID
	Err   error
}

// PartialListError 表示 --agent=all 已完成全部 Agent 读取尝试；成功记录可由 CLI 渲染，但部分 Agent 读取失败。
type PartialListError struct {
	Failures []AgentFailure
}

func (e *PartialListError) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, fmt.Sprintf("agent %q: %v", failure.Agent, failure.Err))
	}
	return "list installed powers failed: " + strings.Join(parts, "; ")
}

// Unwrap 返回部分失败的全部底层错误，使 errors.Is 可匹配每个 Agent 的原因。
func (e *PartialListError) Unwrap() []error {
	causes := make([]error, 0, len(e.Failures))
	for _, failure := range e.Failures {
		causes = append(causes, failure.Err)
	}
	return causes
}

// ListLifecycle 定义 list 命令唯一需要的生命周期入口。
type ListLifecycle interface {
	List(context.Context, Flags) (ListResult, error)
}

// ListLifecycleFactory 在日志初始化后创建 List 生命周期。
type ListLifecycleFactory func() (ListLifecycle, error)
