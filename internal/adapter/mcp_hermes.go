package adapter

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"apm/internal/domain"
)

var ansiControlPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

type hermesMCPAdapter struct {
	runner mcpRunner
}

func newHermesMCPAdapter(runner mcpRunner) *hermesMCPAdapter {
	return &hermesMCPAdapter{runner: runner}
}

func (adapter *hermesMCPAdapter) Inspect(ctx context.Context, name string) (MCPPresence, error) {
	result, runErr := adapter.runner.Run(ctx, "hermes", []string{"mcp", "list"}, "")
	if runErr != nil {
		return inspectUnknown(domain.AgentHermes, name, result, runErr)
	}
	clean := ansiControlPattern.ReplaceAllString(result.Stdout, "")
	if strings.Contains(clean, "No MCP servers configured.") {
		return MCPAbsent, nil
	}
	lines := strings.Split(clean, "\n")
	headerIndex := -1
	for index, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 4 && fields[0] == "Name" && fields[1] == "Transport" && fields[2] == "Tools" && fields[3] == "Status" {
			headerIndex = index
			break
		}
	}
	if headerIndex == -1 {
		return inspectUnknown(domain.AgentHermes, name, result, fmt.Errorf("list table header missing"))
	}
	rows := 0
	matches := 0
	for _, line := range lines[headerIndex+1:] {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "─") {
			continue
		}
		if len(fields) < 4 {
			return inspectUnknown(domain.AgentHermes, name, result, fmt.Errorf("malformed list row %q", line))
		}
		rows++
		if fields[0] == name {
			matches++
		}
	}
	if rows == 0 || matches > 1 {
		return inspectUnknown(domain.AgentHermes, name, result, fmt.Errorf("list rows=%d matches=%d", rows, matches))
	}
	if matches == 1 {
		return MCPPresent, nil
	}
	return MCPAbsent, nil
}

func (adapter *hermesMCPAdapter) Add(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
	if err := validateMCPAdd(ref, server); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	args := []string{"mcp", "add", ref.Name}
	stdin := "\n"
	if server.Transport == domain.MCPTransportHTTP {
		args = append(args, "--url", server.URL)
		stdin = "n\n\n"
	} else {
		args = append(args, "--command", server.Command)
		if len(server.Args) > 0 {
			args = append(args, "--args")
			args = append(args, server.Args...)
		}
	}
	result, runErr := adapter.runner.Run(ctx, "hermes", args, stdin)
	clean := ansiControlPattern.ReplaceAllString(result.Stdout, "")
	success := runErr == nil && strings.Contains(clean, fmt.Sprintf("Saved '%s'", ref.Name))
	return mutationFromCommand(domain.AgentHermes, "add", ref, result, runErr, success)
}

func (adapter *hermesMCPAdapter) Remove(ctx context.Context, ref domain.PowerRef) (MCPMutationResult, error) {
	if err := validateMCPRef(ref); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "hermes", []string{"mcp", "remove", ref.Name}, "\n")
	clean := ansiControlPattern.ReplaceAllString(result.Stdout, "")
	success := runErr == nil && strings.Contains(clean, fmt.Sprintf("Removed '%s'", ref.Name))
	return mutationFromCommand(domain.AgentHermes, "remove", ref, result, runErr, success)
}

var _ MCPAdapter = (*hermesMCPAdapter)(nil)
