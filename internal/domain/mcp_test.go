package domain_test

import (
	"reflect"
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
		{name: "plain HTTP", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "http://example.com/mcp"}, wantErr: true},
		{name: "userinfo", server: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://user:secret@example.com/mcp"}, wantErr: true},
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

func TestDecodeMCPServerJSONPreservesForbiddenFieldPresence(t *testing.T) {
	valid := []struct {
		name string
		body string
		want domain.MCPServer
	}{
		{name: "stdio", body: `{"transport":"stdio","command":"npx","args":[]}`, want: domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{}}},
		{name: "http", body: `{"transport":"http","url":"https://mcp.example.com/mcp"}`, want: domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://mcp.example.com/mcp"}},
	}
	for _, tt := range valid {
		t.Run("valid/"+tt.name, func(t *testing.T) {
			got, err := domain.DecodeMCPServerJSON([]byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("DecodeMCPServerJSON() = %#v, want %#v", got, tt.want)
			}
		})
	}

	invalid := []struct {
		name string
		body string
	}{
		{name: "stdio URL null", body: `{"transport":"stdio","command":"npx","url":null}`},
		{name: "stdio URL empty", body: `{"transport":"stdio","command":"npx","url":""}`},
		{name: "http command null", body: `{"transport":"http","url":"https://mcp.example.com/mcp","command":null}`},
		{name: "http command empty", body: `{"transport":"http","url":"https://mcp.example.com/mcp","command":""}`},
		{name: "http args null", body: `{"transport":"http","url":"https://mcp.example.com/mcp","args":null}`},
		{name: "http args empty", body: `{"transport":"http","url":"https://mcp.example.com/mcp","args":[]}`},
	}
	for _, tt := range invalid {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			if _, err := domain.DecodeMCPServerJSON([]byte(tt.body)); err == nil {
				t.Fatal("DecodeMCPServerJSON() error = nil, want presence-strict union rejection")
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
