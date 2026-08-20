package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"apm/internal/domain"
)

const bibitMCPExecutable = "bianbu-agent"

// bibitMCPAdapter 通过 bianbu-agent CLI 管理 Bibit 的用户级 MCP 配置。
type bibitMCPAdapter struct {
	runner agentCLIRunner
}

func newBibitMCPAdapter(runner agentCLIRunner) *bibitMCPAdapter {
	return &bibitMCPAdapter{runner: runner}
}

func (adapter *bibitMCPAdapter) Inspect(ctx context.Context, name string) (MCPPresence, error) {
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, []string{"mcp", "get", "--json", name}, "")
	if result.StdoutTruncated || result.StderrTruncated {
		return inspectTruncated(domain.AgentBibit, name, result)
	}
	if runErr != nil {
		if result.Stderr == fmt.Sprintf("No MCP server named '%s' found.", name) {
			return MCPAbsent, nil
		}
		return inspectUnknown(domain.AgentBibit, name, result, runErr)
	}

	decoder := json.NewDecoder(bytes.NewBufferString(result.Stdout))
	var document struct {
		Name string `json:"name"`
	}
	if err := decoder.Decode(&document); err != nil {
		return inspectUnknown(domain.AgentBibit, name, result, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected second JSON value")
		}
		return inspectUnknown(domain.AgentBibit, name, result, err)
	}
	if document.Name != name {
		return inspectUnknown(domain.AgentBibit, name, result, fmt.Errorf("returned name %q", document.Name))
	}
	return MCPPresent, nil
}

func (adapter *bibitMCPAdapter) Add(ctx context.Context, ref domain.PowerRef, server domain.MCPServer) (MCPMutationResult, error) {
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
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, args, "")
	return mutationFromCommand(domain.AgentBibit, "add", ref, result, runErr, runErr == nil)
}

func (adapter *bibitMCPAdapter) Remove(ctx context.Context, ref domain.PowerRef) (MCPMutationResult, error) {
	if err := validateMCPRef(ref); err != nil {
		return MCPMutationResult{State: MCPMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, bibitMCPExecutable, []string{"mcp", "remove", ref.Name}, "")
	return mutationFromCommand(domain.AgentBibit, "remove", ref, result, runErr, runErr == nil)
}

var _ MCPAdapter = (*bibitMCPAdapter)(nil)
