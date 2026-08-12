package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"apm/internal/domain"
)

type codexMCPAdapter struct {
	runner mcpRunner
}

func newCodexMCPAdapter(runner mcpRunner) *codexMCPAdapter {
	return &codexMCPAdapter{runner: runner}
}

func (adapter *codexMCPAdapter) Inspect(ctx context.Context, name string) (MCPPresence, error) {
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"mcp", "get", "--json", name}, "")
	if runErr != nil {
		if strings.Contains(result.Stderr, fmt.Sprintf("No MCP server named '%s' found.", name)) {
			return MCPAbsent, nil
		}
		return inspectUnknown(domain.AgentCodex, name, result, runErr)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(result.Stdout))
	var document struct {
		Name string `json:"name"`
	}
	if err := decoder.Decode(&document); err != nil {
		return inspectUnknown(domain.AgentCodex, name, result, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return inspectUnknown(domain.AgentCodex, name, result, err)
	}
	if document.Name != name {
		return inspectUnknown(domain.AgentCodex, name, result, fmt.Errorf("returned name %q", document.Name))
	}
	return MCPPresent, nil
}

func (adapter *codexMCPAdapter) Add(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
	if err := validateMCPAdd(ref, server); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	args := []string{"mcp", "add", ref.Name}
	if server.Transport == domain.MCPTransportHTTP {
		args = append(args, "--url", server.URL)
	} else {
		args = append(args, "--", server.Command)
		args = append(args, server.Args...)
	}
	result, runErr := adapter.runner.Run(ctx, "codex", args, "")
	return mutationFromCommand(domain.AgentCodex, "add", ref, result, runErr, runErr == nil)
}

func (adapter *codexMCPAdapter) Remove(ctx context.Context, ref domain.PowerRef) (MCPMutationResult, error) {
	if err := validateMCPRef(ref); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"mcp", "remove", ref.Name}, "")
	return mutationFromCommand(domain.AgentCodex, "remove", ref, result, runErr, runErr == nil)
}

var _ MCPAdapter = (*codexMCPAdapter)(nil)
