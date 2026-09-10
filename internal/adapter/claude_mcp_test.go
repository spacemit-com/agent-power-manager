package adapter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/domain"
)

func TestClaudeMCPAdapterUsesOnlyUserScope(t *testing.T) {
	runner := &recordingMCPRunner{}
	subject := newClaudeMCPAdapter(runner)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}

	_, err := subject.Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = subject.Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = subject.Remove(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	want := []recordedMCPCommand{
		{executable: "claude", args: []string{"mcp", "add", "--scope", "user", "demo", "--", "npx", "-y", "pkg"}},
		{executable: "claude", args: []string{"mcp", "add", "--scope", "user", "--transport", "http", "demo", "https://example.com/mcp"}},
		{executable: "claude", args: []string{"mcp", "remove", "--scope", "user", "demo"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestClaudeMCPInspectRejectsNonUserOrUnclearScope(t *testing.T) {
	tests := []struct {
		name     string
		response queuedMCPResponse
		want     MCPPresence
		wantErr  error
	}{
		{name: "user present", response: successfulMCPResponse("demo:\n  Scope: User config (available in all your projects)\n  Status: Connected\n  Type: stdio\n"), want: MCPPresent},
		{name: "user present legacy exact scope", response: successfulMCPResponse("demo:\n  Scope: User\n  Status: Connected\n  Type: stdio\n"), want: MCPPresent},
		{name: "absent", response: failedMCPResponse(`No MCP server found with name: "demo". No MCP servers are configured.`), want: MCPAbsent},
		{name: "local only", response: successfulMCPResponse("demo:\n  Scope: Local config (private to you in this project)\n  Status: Connected\n"), wantErr: ErrMCPStateUnknown},
		{name: "project only", response: successfulMCPResponse("demo:\n  Scope: Project config (shared via .mcp.json)\n  Status: Connected\n"), wantErr: ErrMCPStateUnknown},
		{name: "missing scope", response: successfulMCPResponse("demo:\n  Status: Connected\n"), wantErr: ErrMCPStateUnknown},
		{name: "unrelated user scope", response: successfulMCPResponse("demo:\n  Scope: Local\nother:\n  Scope: User\n"), wantErr: ErrMCPStateUnknown},
		{name: "duplicate target section", response: successfulMCPResponse("demo:\n  Scope: User\ndemo:\n  Scope: User\n"), wantErr: ErrMCPStateUnknown},
		{name: "contradictory target scopes", response: successfulMCPResponse("demo:\n  Scope: User\n  Scope: Local\n"), wantErr: ErrMCPStateUnknown},
		{name: "truncated valid prefix", response: truncatedMCPResponse("demo:\n  Scope: User\n"), wantErr: ErrMCPStateUnknown},
		{name: "health failure", response: failedMCPResponse("demo: Failed to connect"), wantErr: ErrMCPStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			presence, err := newClaudeMCPAdapter(runner).Inspect(context.Background(), "demo")
			if presence != tt.want || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Inspect() = %q, %v; want %q, %v", presence, err, tt.want, tt.wantErr)
			}
			want := []recordedMCPCommand{{executable: "claude", args: []string{"mcp", "get", "demo"}}}
			if !reflect.DeepEqual(runner.commands, want) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, want)
			}
		})
	}
}
