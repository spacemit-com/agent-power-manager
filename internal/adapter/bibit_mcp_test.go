package adapter

import (
	"context"
	"errors"
	"testing"
)

func TestBibitMCPAdapterCommands(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		{result: agentCLIResult{Stdout: `{"name":"demo"}`}},
		{}, {}, {},
	}}
	subject := newBibitMCPAdapter(runner)
	_, _ = subject.Inspect(context.Background(), "demo")
	_, _ = subject.Add(context.Background(), mcpRef("demo"), stdioServer("npx", "-y", "pkg"))
	_, _ = subject.Add(context.Background(), mcpRef("demo"), httpServer("https://example.com/mcp"))
	_, _ = subject.Remove(context.Background(), mcpRef("demo"))
	assertMCPCommands(t, runner, "bianbu-agent", [][]string{
		{"mcp", "get", "--json", "demo"},
		{"mcp", "add", "demo", "--", "npx", "-y", "pkg"},
		{"mcp", "add", "demo", "--url", "https://example.com/mcp"},
		{"mcp", "remove", "demo"},
	})
}

func TestBibitMCPInspectAcceptsOnlyUnambiguousResults(t *testing.T) {
	tests := []struct {
		name     string
		response queuedMCPResponse
		want     MCPPresence
		wantErr  error
	}{
		{name: "present", response: successfulMCPResponse(`{"name":"demo"}`), want: MCPPresent},
		{name: "exact absent stderr", response: failedMCPResponse("No MCP server named 'demo' found."), want: MCPAbsent},
		{name: "absent stderr with extra output", response: failedMCPResponse("warning\nNo MCP server named 'demo' found."), wantErr: ErrMCPStateUnknown},
		{name: "malformed JSON", response: successfulMCPResponse(`{"name":`), wantErr: ErrMCPStateUnknown},
		{name: "second JSON value", response: successfulMCPResponse(`{"name":"demo"} {}`), wantErr: ErrMCPStateUnknown},
		{name: "wrong name", response: successfulMCPResponse(`{"name":"other"}`), wantErr: ErrMCPStateUnknown},
		{name: "stdout truncated", response: truncatedMCPResponse(`{"name":"demo"}`), wantErr: ErrMCPStateUnknown},
		{name: "stderr truncated", response: queuedMCPResponse{result: agentCLIResult{Path: "/fake/bianbu-agent", Stdout: `{"name":"demo"}`, Started: true, StderrTruncated: true}}, wantErr: ErrMCPStateUnknown},
		{name: "CLI unavailable", response: queuedMCPResponse{err: ErrAgentCLIUnavailable}, wantErr: ErrMCPStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			presence, err := newBibitMCPAdapter(runner).Inspect(context.Background(), "demo")
			if presence != tt.want || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Inspect() = %q, %v; want %q, %v", presence, err, tt.want, tt.wantErr)
			}
			assertMCPCommands(t, runner, "bianbu-agent", [][]string{{"mcp", "get", "--json", "demo"}})
		})
	}
}

func TestBibitMCPMutationMapsProcessOutcomes(t *testing.T) {
	tests := []struct {
		name      string
		response  queuedMCPResponse
		wantState MCPMutationState
		wantErr   error
	}{
		{name: "applied", response: successfulMCPResponse("configured"), wantState: MCPMutationApplied},
		{name: "unknown after process starts", response: failedMCPResponse("write failed"), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "not applied when CLI unavailable", response: queuedMCPResponse{err: ErrAgentCLIUnavailable}, wantState: MCPMutationNotApplied, wantErr: ErrAgentCLIUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			result, err := newBibitMCPAdapter(runner).Remove(context.Background(), mcpRef("demo"))
			if result.State != tt.wantState || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Remove() = %+v, %v; want %q, %v", result, err, tt.wantState, tt.wantErr)
			}
		})
	}
}
