package adapter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/domain"
)

func TestHermesMCPAdapterUsesExactArgvAndScriptedInput(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse("Saved 'demo' to config"),
		successfulMCPResponse("Saved 'demo' to config"),
		successfulMCPResponse("Removed 'demo' from config"),
	}}
	subject := newHermesMCPAdapter(runner)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}

	for _, operation := range []func() (MCPMutationResult, error){
		func() (MCPMutationResult, error) {
			return subject.Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}})
		},
		func() (MCPMutationResult, error) {
			return subject.Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://example.com/mcp"})
		},
		func() (MCPMutationResult, error) { return subject.Remove(context.Background(), ref) },
	} {
		result, err := operation()
		if err != nil || result.State != MCPMutationApplied {
			t.Fatalf("mutation = %+v, %v", result, err)
		}
	}
	want := []recordedMCPCommand{
		{executable: "hermes", args: []string{"mcp", "add", "demo", "--command", "npx", "--args", "-y", "pkg"}, stdin: "\n"},
		{executable: "hermes", args: []string{"mcp", "add", "demo", "--url", "https://example.com/mcp"}, stdin: "n\n\n"},
		{executable: "hermes", args: []string{"mcp", "remove", "demo"}, stdin: "\n"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestHermesMCPAdapterOmitsArgsFlagWhenEmpty(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse("Saved 'demo' to config")}}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
	_, err := newHermesMCPAdapter(runner).Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "node"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mcp", "add", "demo", "--command", "node"}
	if !reflect.DeepEqual(runner.commands[0].args, want) {
		t.Fatalf("args = %#v, want %#v", runner.commands[0].args, want)
	}
}

func TestHermesMCPInspectStrictlyParsesListTable(t *testing.T) {
	tests := []struct {
		name     string
		response queuedMCPResponse
		want     MCPPresence
		wantErr  error
	}{
		{name: "present", response: successfulMCPResponse("\n  MCP Servers:\n\n  Name             Transport                      Tools        Status\n  ──────────────── ────────────────────────────── ──────────── ──────────\n  demo             npx -y pkg                     all          \x1b[32m✓ enabled\x1b[0m\n"), want: MCPPresent},
		{name: "absent among others", response: successfulMCPResponse("\n  MCP Servers:\n\n  Name             Transport                      Tools        Status\n  ──────────────── ────────────────────────────── ──────────── ──────────\n  other            npx                            all          ✓ enabled\n"), want: MCPAbsent},
		{name: "empty", response: successfulMCPResponse("No MCP servers configured."), want: MCPAbsent},
		{name: "duplicate", response: successfulMCPResponse("MCP Servers:\nName Transport Tools Status\ndemo npx all enabled\ndemo node all enabled\n"), wantErr: ErrMCPStateUnknown},
		{name: "missing header", response: successfulMCPResponse("demo npx all enabled\n"), wantErr: ErrMCPStateUnknown},
		{name: "truncated valid table", response: truncatedMCPResponse("MCP Servers:\nName Transport Tools Status\ndemo npx all enabled\n"), wantErr: ErrMCPStateUnknown},
		{name: "failure", response: failedMCPResponse("config unreadable"), wantErr: ErrMCPStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{tt.response}}
			presence, err := newHermesMCPAdapter(runner).Inspect(context.Background(), "demo")
			if presence != tt.want || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Inspect() = %q, %v; want %q, %v", presence, err, tt.want, tt.wantErr)
			}
			want := []recordedMCPCommand{{executable: "hermes", args: []string{"mcp", "list"}}}
			if !reflect.DeepEqual(runner.commands, want) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, want)
			}
		})
	}
}

func TestHermesMCPMutationRequiresExplicitSuccessMarker(t *testing.T) {
	tests := []queuedMCPResponse{
		successfulMCPResponse("Connected with 3 tools"),
		truncatedMCPResponse("Saved 'demo' to config"),
		failedMCPResponse("Failed to connect"),
	}
	for index, response := range tests {
		runner := &recordingMCPRunner{responses: []queuedMCPResponse{response}}
		ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
		result, err := newHermesMCPAdapter(runner).Add(context.Background(), ref, domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx"})
		if result.State != MCPMutationUnknown || !errors.Is(err, ErrMCPMutationUnknown) {
			t.Fatalf("case %d Add() = %+v, %v; want unknown", index, result, err)
		}
	}
}
