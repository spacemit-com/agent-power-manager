package adapter

import (
	"context"

	"apm/internal/domain"
)

// MCPPresence 表示目标 Agent CLI 已明确确认的同名 MCP 状态。
type MCPPresence string

const (
	// MCPPresent 表示同名 MCP 明确存在。
	MCPPresent MCPPresence = "present"
	// MCPAbsent 表示同名 MCP 明确不存在。
	MCPAbsent MCPPresence = "absent"
)

// MCPMutationState 表示 Agent CLI 写操作产生副作用的确定程度。
type MCPMutationState string

const (
	// MCPMutationApplied 表示 CLI 已明确完成变更。
	MCPMutationApplied MCPMutationState = "applied"
	// MCPMutationNotApplied 表示 CLI 明确没有产生变更。
	MCPMutationNotApplied MCPMutationState = "not-applied"
	// MCPMutationUnknown 表示无法判断 CLI 是否已经产生变更，调用方必须保留 journal。
	MCPMutationUnknown MCPMutationState = "unknown"
)

// MCPMutationResult 描述一次 MCP CLI 写操作的副作用事实。
type MCPMutationResult struct {
	State MCPMutationState
}

// MCPAdapter 通过目标 Agent CLI 检查、添加和删除用户级 MCP 配置。
type MCPAdapter interface {
	Inspect(context.Context, string) (MCPPresence, error)
	Add(context.Context, domain.PowerRef, domain.MCPServer) (MCPMutationResult, error)
	Remove(context.Context, domain.PowerRef) (MCPMutationResult, error)
}
