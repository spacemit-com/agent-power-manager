package adapter

import (
	"context"
	"fmt"
	"strings"

	"apm/internal/domain"
)

type claudeMCPAdapter struct {
	runner mcpRunner
}

func newClaudeMCPAdapter(runner mcpRunner) *claudeMCPAdapter {
	return &claudeMCPAdapter{runner: runner}
}

func (adapter *claudeMCPAdapter) Inspect(ctx context.Context, name string) (MCPPresence, error) {
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"mcp", "get", name}, "")
	combined := result.Stdout + "\n" + result.Stderr
	if runErr != nil {
		if strings.Contains(combined, "No MCP server found with name: "+name) {
			return MCPAbsent, nil
		}
		return inspectUnknown(domain.AgentClaudeCode, name, result, runErr)
	}
	lines := strings.Split(result.Stdout, "\n")
	foundName := false
	foundUserScope := false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case name + ":":
			foundName = true
		case "Scope: User":
			foundUserScope = true
		}
	}
	if !foundName || !foundUserScope {
		return inspectUnknown(domain.AgentClaudeCode, name, result, fmt.Errorf("name or user scope missing"))
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
