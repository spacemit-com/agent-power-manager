package adapter

import (
	"context"
	"errors"
	"testing"
)

func TestBibitMCPAdapterCommands(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		{result: agentCLIResult{Stdout: `{"id":"mcp.demo"}`}},
		successfulMCPResponse(`{"id":"mcp.demo"}`),
		successfulMCPResponse(`{"deleted":true}`),
	}}
	subject := newBibitMCPAdapter(runner)
	if _, err := subject.Inspect(context.Background(), "demo"); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if result, err := subject.Add(context.Background(), mcpRef("demo"), stdioServer("npx", "-y", "pkg")); err != nil || result.State != MCPMutationApplied {
		t.Fatalf("Add(stdio) = %+v, %v; want applied", result, err)
	}
	if result, err := subject.Remove(context.Background(), mcpRef("demo")); err != nil || result.State != MCPMutationApplied {
		t.Fatalf("Remove() = %+v, %v; want applied", result, err)
	}
	assertMCPCommands(t, runner, "bianbu-agent", [][]string{
		{"mcp", "get", "demo"},
		{"mcp", "add", "demo", "--command", "npx", "--args", "-y", "pkg"},
		{"mcp", "remove", "demo"},
	})
}

func TestBibitMCPAdapterAddHTTPUsesAddURL(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`{"content_digest":"fake","id":"mcp.demo"}`),
	}}
	subject := newBibitMCPAdapter(runner)

	result, err := subject.Add(context.Background(), mcpRef("demo"), httpServer("https://example.com/mcp"))
	if err != nil || result.State != MCPMutationApplied {
		t.Fatalf("Add(http) = %+v, %v; want applied", result, err)
	}
	// HTTP 直接经 mcp add --url 登记，不再写临时目录包或调用 mcp install。
	assertMCPCommands(t, runner, "bianbu-agent", [][]string{
		{"mcp", "add", "demo", "--url", "https://example.com/mcp", "--auth", "none"},
	})
}

func TestBibitMCPAdapterAddStdioOmitsEmptyArgs(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`{"id":"mcp.demo"}`),
	}}
	subject := newBibitMCPAdapter(runner)

	result, err := subject.Add(context.Background(), mcpRef("demo"), stdioServer("node"))
	if err != nil || result.State != MCPMutationApplied {
		t.Fatalf("Add(stdio) = %+v, %v; want applied", result, err)
	}
	assertMCPCommands(t, runner, "bianbu-agent", [][]string{
		{"mcp", "add", "demo", "--command", "node"},
	})
}

func TestBibitMCPInspectAcceptsOnlyUnambiguousResults(t *testing.T) {
	tests := []struct {
		name     string
		response queuedMCPResponse
		want     MCPPresence
		wantErr  error
	}{
		{name: "present", response: successfulMCPResponse(`{"id":"mcp.demo"}`), want: MCPPresent},
		{name: "absent null", response: successfulMCPResponse("null"), want: MCPAbsent},
		{name: "absent null with trailing newline", response: successfulMCPResponse("null\n"), want: MCPAbsent},
		{name: "wrong id", response: successfulMCPResponse(`{"id":"mcp.other"}`), wantErr: ErrMCPStateUnknown},
		{name: "missing id", response: successfulMCPResponse(`{"content_digest":"fake"}`), wantErr: ErrMCPStateUnknown},
		{name: "malformed JSON", response: successfulMCPResponse(`{"id":`), wantErr: ErrMCPStateUnknown},
		{name: "second JSON value", response: successfulMCPResponse(`{"id":"mcp.demo"} {}`), wantErr: ErrMCPStateUnknown},
		{name: "stdout truncated", response: truncatedMCPResponse(`{"id":"mcp.demo"}`), wantErr: ErrMCPStateUnknown},
		{name: "stderr truncated", response: queuedMCPResponse{result: agentCLIResult{Path: "/fake/bianbu-agent", Stdout: `{"id":"mcp.demo"}`, Started: true, StderrTruncated: true}}, wantErr: ErrMCPStateUnknown},
		{name: "CLI unavailable", response: queuedMCPResponse{err: ErrAgentCLIUnavailable}, wantErr: ErrMCPStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			presence, err := newBibitMCPAdapter(runner).Inspect(context.Background(), "demo")
			if presence != tt.want || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Inspect() = %q, %v; want %q, %v", presence, err, tt.want, tt.wantErr)
			}
			assertMCPCommands(t, runner, "bianbu-agent", [][]string{{"mcp", "get", "demo"}})
		})
	}
}

func TestBibitMCPAddAcceptsOnlyConfirmedRegistration(t *testing.T) {
	tests := []struct {
		name      string
		response  queuedMCPResponse
		wantState MCPMutationState
		wantErr   error
	}{
		{name: "applied when id matches", response: successfulMCPResponse(`{"id":"mcp.demo"}`), wantState: MCPMutationApplied},
		{name: "unknown on mismatched id", response: successfulMCPResponse(`{"id":"mcp.other"}`), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "unknown on missing id", response: successfulMCPResponse(`{"deleted":true}`), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "unknown on plain text output", response: successfulMCPResponse("configured"), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "unknown on second JSON value", response: successfulMCPResponse(`{"id":"mcp.demo"} {}`), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "unknown on truncated stdout", response: truncatedMCPResponse(`{"id":"mcp.demo"}`), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "unknown after process starts", response: failedMCPResponse("MCP server already exists: mcp.demo"), wantState: MCPMutationUnknown, wantErr: ErrMCPMutationUnknown},
		{name: "not applied when CLI unavailable", response: queuedMCPResponse{err: ErrAgentCLIUnavailable}, wantState: MCPMutationNotApplied, wantErr: ErrAgentCLIUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			result, err := newBibitMCPAdapter(runner).Add(context.Background(), mcpRef("demo"), httpServer("https://example.com/mcp"))
			if result.State != tt.wantState || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Add() = %+v, %v; want %q, %v", result, err, tt.wantState, tt.wantErr)
			}
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
		{name: "applied", response: successfulMCPResponse(`{"deleted":true}`), wantState: MCPMutationApplied},
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
