package adapter

import (
	"context"
	"fmt"
	"strings"

	"apm/internal/domain"
)

type claudeMCPAdapter struct {
	runner agentCLIRunner
}

func newClaudeMCPAdapter(runner agentCLIRunner) *claudeMCPAdapter {
	return &claudeMCPAdapter{runner: runner}
}

func (adapter *claudeMCPAdapter) Inspect(ctx context.Context, name string) (MCPPresence, error) {
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"mcp", "get", name}, "")
	if result.StdoutTruncated || result.StderrTruncated {
		return inspectTruncated(domain.AgentClaudeCode, name, result)
	}
	combined := result.Stdout + "\n" + result.Stderr
	if runErr != nil {
		// 真实 CLI 的 not-found 消息给 name 加引号：No MCP server found with name: "demo".
		if strings.Contains(combined, `No MCP server found with name: "`+name+`"`) {
			return MCPAbsent, nil
		}
		return inspectUnknown(domain.AgentClaudeCode, name, result, runErr)
	}
	targetSections := 0
	targetScopes := 0
	inTargetSection := false
	for _, line := range strings.Split(result.Stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, ":") && strings.Count(trimmed, ":") == 1 {
			inTargetSection = trimmed == name+":"
			if inTargetSection {
				targetSections++
			}
			continue
		}
		if !inTargetSection || !strings.HasPrefix(trimmed, "Scope:") {
			continue
		}
		targetScopes++
		// 真实 CLI 的 scope 行形如 "Scope: User config (available in all your projects)"；
		// 取 "Scope:" 后的首个空白分隔 token，仅当为 "User" 才算 user scope。
		scope := strings.TrimSpace(strings.TrimPrefix(trimmed, "Scope:"))
		fields := strings.Fields(scope)
		if len(fields) == 0 || fields[0] != "User" {
			return inspectUnknown(domain.AgentClaudeCode, name, result, fmt.Errorf("target section has non-user scope %q", trimmed))
		}
	}
	if targetSections != 1 || targetScopes != 1 {
		return inspectUnknown(domain.AgentClaudeCode, name, result, fmt.Errorf("target sections=%d scopes=%d", targetSections, targetScopes))
	}
	return MCPPresent, nil
}

func (adapter *claudeMCPAdapter) Add(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
	if err := validateMCPAdd(ref, server); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	args := []string{"mcp", "add", "--scope", "user"}
	if server.Transport == domain.MCPTransportHTTP {
		args = append(args, "--transport", "http", ref.Name, server.URL)
	} else {
		args = append(args, ref.Name, "--", server.Command)
		args = append(args, server.Args...)
	}
	result, runErr := adapter.runner.Run(ctx, "claude", args, "")
	return mutationFromCommand(domain.AgentClaudeCode, "add", ref, result, runErr, runErr == nil)
}

func (adapter *claudeMCPAdapter) Remove(ctx context.Context, ref domain.PowerRef) (MCPMutationResult, error) {
	if err := validateMCPRef(ref); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"mcp", "remove", "--scope", "user", ref.Name}, "")
	return mutationFromCommand(domain.AgentClaudeCode, "remove", ref, result, runErr, runErr == nil)
}

var _ MCPAdapter = (*claudeMCPAdapter)(nil)
