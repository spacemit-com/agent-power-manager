package domain_test

import (
	"testing"

	"apm/internal/domain"
)

func TestMCPServerValidateAcceptsOnlyStdioOrHTTP(t *testing.T) {
	tests := []struct {
		name    string
		server  domain.MCPServer
		wantErr bool
	}{
		{name: "stdio", server: domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}},
		{name: "stdio empty args", server: domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "node"}},
		{name: "http", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://mcp.example.com/mcp"}},
		{name: "blank command", server: domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "  "}, wantErr: true},
		{name: "stdio url", server: domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", URL: "https://example.com"}, wantErr: true},
		{name: "http command", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://example.com", Command: "npx"}, wantErr: true},
		{name: "http args", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://example.com", Args: []string{"x"}}, wantErr: true},
		{name: "relative url", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "/mcp"}, wantErr: true},
		{name: "missing host", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https:///mcp"}, wantErr: true},
		{name: "unsupported scheme", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "ftp://example.com/mcp"}, wantErr: true},
		{name: "unknown transport", server: domain.MCPServer{Transport: "sse", URL: "https://example.com"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.server.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMCPServerCloneDoesNotShareArguments(t *testing.T) {
	source := domain.MCPServer{
		Transport: domain.MCPTransportStdio,
		Command:   "npx",
		Args:      []string{"one"},
	}
	clone := source.Clone()
	clone.Args[0] = "changed"
	if source.Args[0] != "one" {
		t.Fatalf("Clone() shared Args storage with source: %#v", source)
	}
}
