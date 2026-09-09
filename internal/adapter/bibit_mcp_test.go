package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestBibitMCPAdapterCommands(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		{result: agentCLIResult{Stdout: `{"id":"mcp.demo"}`}},
		{}, {},
	}}
	subject := newBibitMCPAdapter(runner)
	_, _ = subject.Inspect(context.Background(), "demo")
	_, _ = subject.Add(context.Background(), mcpRef("demo"), stdioServer("npx", "-y", "pkg"))
	_, _ = subject.Remove(context.Background(), mcpRef("demo"))
	assertMCPCommands(t, runner, "bianbu-agent", [][]string{
		{"mcp", "get", "demo"},
		{"mcp", "add", "demo", "--", "npx", "-y", "pkg"},
		{"mcp", "remove", "demo"},
	})
}

func TestBibitMCPAdapterAddHTTPInstallsPackage(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`{"content_digest":"fake","id":"mcp.demo"}`),
	}}
	var created []string
	var files []string
	subject := newBibitMCPAdapter(runner)
	subject.mkdtemp = func(_, _ string) (string, error) { return "/tmp/apm-bibit-mcp-http", nil }
	subject.mkdirAll = func(path string, _ os.FileMode) error { created = append(created, path); return nil }
	subject.writeFile = func(_ string, data []byte, _ os.FileMode) error { files = append(files, string(data)); return nil }
	subject.removeAll = func(_ string) error { return nil }

	result, err := subject.Add(context.Background(), mcpRef("demo"), httpServer("https://example.com/mcp"))
	if err != nil || result.State != MCPMutationApplied {
		t.Fatalf("Add(http) = %+v, %v; want applied", result, err)
	}
	assertMCPCommands(t, runner, "bianbu-agent", [][]string{
		{"mcp", "install", "/tmp/apm-bibit-mcp-http/mcp.demo"},
	})
	if len(created) != 1 || created[0] != "/tmp/apm-bibit-mcp-http/mcp.demo" {
		t.Fatalf("created dirs = %#v, want single package directory", created)
	}
	if len(files) != 1 {
		t.Fatalf("written files = %d, want 1", len(files))
	}
	var document struct {
		DisplayName string  `json:"display_name"`
		Transport   string  `json:"transport"`
		URL         string  `json:"url"`
		Timeout     float64 `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(files[0]), &document); err != nil {
		t.Fatalf("mcp.json is invalid: %v", err)
	}
	if document.DisplayName != "demo" || document.Transport != "streamable-http" || document.URL != "https://example.com/mcp" || document.Timeout != 30.0 {
		t.Fatalf("mcp.json = %#v", document)
	}
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
