package adapter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/domain"
)

func TestCodexMCPAdapterBuildsExactMutationArgv(t *testing.T) {
	runner := &recordingMCPRunner{}
	subject := newCodexMCPAdapter(runner)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}

	stdio, err := subject.Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}})
	if err != nil || stdio.State != MCPMutationApplied {
		t.Fatalf("stdio Add() = %+v, %v", stdio, err)
	}
	http, err := subject.Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://example.com/mcp"})
	if err != nil || http.State != MCPMutationApplied {
		t.Fatalf("HTTP Add() = %+v, %v", http, err)
	}
	removed, err := subject.Remove(context.Background(), ref)
	if err != nil || removed.State != MCPMutationApplied {
		t.Fatalf("Remove() = %+v, %v", removed, err)
	}
	want := []recordedMCPCommand{
		{executable: "codex", args: []string{"mcp", "add", "demo", "--", "npx", "-y", "pkg"}},
		{executable: "codex", args: []string{"mcp", "add", "demo", "--url", "https://example.com/mcp"}},
		{executable: "codex", args: []string{"mcp", "remove", "demo"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestCodexMCPInspectRequiresExactStructuredName(t *testing.T) {
	tests := []struct {
		name     string
		response queuedMCPResponse
		want     MCPPresence
		wantErr  error
	}{
		{name: "present", response: successfulMCPResponse(`{"name":"demo","transport":{"type":"stdio","command":"npx","args":[]},"enabled":true}`), want: MCPPresent},
		{name: "absent", response: failedMCPResponse("Error: No MCP server named 'demo' found."), want: MCPAbsent},
		{name: "wrong name", response: successfulMCPResponse(`{"name":"other"}`), wantErr: ErrMCPStateUnknown},
		{name: "invalid JSON", response: successfulMCPResponse(`{"name":`), wantErr: ErrMCPStateUnknown},
		{name: "trailing JSON", response: successfulMCPResponse(`{"name":"demo"} {}`), wantErr: ErrMCPStateUnknown},
		{name: "truncated valid JSON", response: truncatedMCPResponse(`{"name":"demo"}`), wantErr: ErrMCPStateUnknown},
		{name: "ambiguous failure", response: failedMCPResponse("permission denied"), wantErr: ErrMCPStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			presence, err := newCodexMCPAdapter(runner).Inspect(context.Background(), "demo")
			if presence != tt.want || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Inspect() = %q, %v; want %q, %v", presence, err, tt.want, tt.wantErr)
			}
			wantCommand := recordedMCPCommand{executable: "codex", args: []string{"mcp", "get", "--json", "demo"}}
			if !reflect.DeepEqual(runner.commands, []recordedMCPCommand{wantCommand}) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommand)
			}
		})
	}
}

func TestCodexMCPMutationFailsClosedAfterProcessStarts(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{failedMCPResponse("write failed")}}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
	result, err := newCodexMCPAdapter(runner).Remove(context.Background(), ref)
	if result.State != MCPMutationUnknown || !errors.Is(err, ErrMCPMutationUnknown) {
		t.Fatalf("Remove() = %+v, %v; want unknown", result, err)
	}
}
