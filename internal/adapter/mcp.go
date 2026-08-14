package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

func validateMCPRef(ref domain.PowerRef) error {
	if ref.Type != domain.PowerTypeMCP || strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Version) == "" {
		return fmt.Errorf("MCP reference %+v: %w", ref, ErrInvalidMCP)
	}
	return nil
}

func validateMCPAdd(ref domain.PowerRef, server domain.MCPServer) error {
	if err := validateMCPRef(ref); err != nil {
		return err
	}
	if err := server.Validate(); err != nil {
		return errors.Join(ErrInvalidMCP, err)
	}
	return nil
}

func mutationFromCommand(agent domain.AgentID, operation string, ref domain.PowerRef, result agentCLIResult, runErr error, success bool) (MCPMutationResult, error) {
	if !result.Started {
		return MCPMutationResult{State: MCPMutationNotApplied}, fmt.Errorf("%s MCP %s %q did not start: %w", agent, operation, ref.Name, runErr)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		runErr = errors.Join(runErr, fmt.Errorf(
			"Agent CLI output was truncated (stdout=%t, stderr=%t)",
			result.StdoutTruncated,
			result.StderrTruncated,
		))
		success = false
	}
	if runErr == nil && success {
		return MCPMutationResult{State: MCPMutationApplied}, nil
	}
	diagnostic := strings.TrimSpace(result.Stderr)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(result.Stdout)
	}
	return MCPMutationResult{State: MCPMutationUnknown}, fmt.Errorf(
		"%s MCP %s %q via %q has unknown result (exit=%d, diagnostic=%q): %w",
		agent, operation, ref.Name, result.Path, result.ExitCode, diagnostic,
		errors.Join(ErrMCPMutationUnknown, runErr),
	)
}

func inspectUnknown(agent domain.AgentID, name string, result agentCLIResult, err error) (MCPPresence, error) {
	diagnostic := strings.TrimSpace(result.Stderr)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(result.Stdout)
	}
	return "", fmt.Errorf(
		"inspect %s MCP %q via %q (exit=%d, diagnostic=%q): %w",
		agent, name, result.Path, result.ExitCode, diagnostic,
		errors.Join(ErrMCPStateUnknown, err),
	)
}

func inspectTruncated(agent domain.AgentID, name string, result agentCLIResult) (MCPPresence, error) {
	return inspectUnknown(agent, name, result, fmt.Errorf(
		"Agent CLI output was truncated (stdout=%t, stderr=%t)",
		result.StdoutTruncated,
		result.StderrTruncated,
	))
}
