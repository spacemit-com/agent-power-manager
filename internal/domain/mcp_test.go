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
		{name: "transport null", body: `{"transport":null,"command":"npx"}`},
		{name: "stdio command null", body: `{"transport":"stdio","command":null}`},
		{name: "stdio args null", body: `{"transport":"stdio","command":"npx","args":null}`},
		{name: "stdio URL null", body: `{"transport":"stdio","command":"npx","url":null}`},
		{name: "stdio URL empty", body: `{"transport":"stdio","command":"npx","url":""}`},
		{name: "http command null", body: `{"transport":"http","url":"https://mcp.example.com/mcp","command":null}`},
		{name: "http command empty", body: `{"transport":"http","url":"https://mcp.example.com/mcp","command":""}`},
		{name: "http args null", body: `{"transport":"http","url":"https://mcp.example.com/mcp","args":null}`},
		{name: "http args empty", body: `{"transport":"http","url":"https://mcp.example.com/mcp","args":[]}`},
		{name: "http URL null", body: `{"transport":"http","url":null}`},
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

func TestDecodeMCPResourceJSONValidatesCanonicalResource(t *testing.T) {
	raw := []byte(`{"schema_version":1,"name":"filesystem","version":"v1.0.0","description":"files","definition":{"transport":"stdio","command":"npx","args":["-y","pkg"]}}`)
	got, err := domain.DecodeMCPResourceJSON(raw)
	if err != nil {
		t.Fatalf("DecodeMCPResourceJSON() error = %v", err)
	}
	if got.Name != "filesystem" || got.Version != "v1.0.0" || got.Definition.Command != "npx" {
		t.Fatalf("DecodeMCPResourceJSON() = %+v", got)
	}
}

func TestDecodeMCPResourceJSONToleratesServerExtraFields(t *testing.T) {
	// 测试环境资源文件额外返回 type/message/content_md5，其中 message 与 content_md5 为 null；容忍但不校验。
	raw := []byte(`{"type":"mcp","name":"filesystem","version":"v1.0.0","description":"提供受限的文件系统访问能力","message":null,"definition":{"transport":"stdio","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem"]},"schema_version":1,"content_md5":null}`)
	got, err := domain.DecodeMCPResourceJSON(raw)
	if err != nil {
		t.Fatalf("DecodeMCPResourceJSON() error = %v", err)
	}
	if got.Name != "filesystem" || got.Version != "v1.0.0" || got.Definition.Command != "npx" {
		t.Fatalf("DecodeMCPResourceJSON() = %+v", got)
	}
}

func TestDecodeMCPResourceJSONRejectsOldAndOpenShapes(t *testing.T) {
	cases := []string{
		`{"transport":"stdio","command":"npx"}`,
		`{"schema_version":1,"name":"demo","name":"shadow","version":"v1.0.0","description":"d","definition":{"transport":"stdio","command":"npx"}}`,
		`{"schema_version":1,"name":"demo","version":"v1.0.0","description":"d","definition":{"transport":"stdio","command":"npx"},"extra":true}`,
	}
	for _, raw := range cases {
		if _, err := domain.DecodeMCPResourceJSON([]byte(raw)); err == nil {
			t.Fatalf("DecodeMCPResourceJSON() accepted %s", raw)
		}
	}
}

func TestMCPResourceValidateRejectsInvalidIdentityAndDefinition(t *testing.T) {
	validDefinition := `{"transport":"stdio","command":"npx"}`
	cases := []struct {
		name string
		raw  string
	}{
		{name: "unsupported schema version", raw: `{"schema_version":3,"name":"demo","version":"v1.0.0","description":"d","definition":` + validDefinition + `}`},
		{name: "blank name_zh", raw: `{"schema_version":2,"name":"demo","version":"v1.0.0","description":"d","name_zh":"  ","definition":` + validDefinition + `}`},
		{name: "noncanonical name", raw: `{"schema_version":1,"name":"Demo","version":"v1.0.0","description":"d","definition":` + validDefinition + `}`},
		{name: "noncanonical version", raw: `{"schema_version":1,"name":"demo","version":"1.0.0","description":"d","definition":` + validDefinition + `}`},
		{name: "blank description", raw: `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"  ","definition":` + validDefinition + `}`},
		{name: "stdio without command", raw: `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"d","definition":{"transport":"stdio"}}`},
		{name: "http with stdio field", raw: `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"d","definition":{"transport":"http","url":"https://mcp.example.com/mcp","args":[]}}`},
		{name: "null definition", raw: `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"d","definition":null}`},
		{name: "tail JSON", raw: `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"d","definition":` + validDefinition + `} {}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := domain.DecodeMCPResourceJSON([]byte(tt.raw)); err == nil {
				t.Fatalf("DecodeMCPResourceJSON() accepted invalid resource %s", tt.raw)
			}
		})
	}
}

func TestMCPResourceValidatePowerRefRequiresExactIdentity(t *testing.T) {
	resource := domain.MCPResource{Name: "filesystem", Version: "v1.0.0"}
	if err := resource.ValidatePowerRef(domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v1.0.0"}); err != nil {
		t.Fatalf("ValidatePowerRef() error = %v", err)
	}
	if err := resource.ValidatePowerRef(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "filesystem", Version: "v1.0.0"}); err == nil {
		t.Fatal("ValidatePowerRef() accepted a different type")
	}
	if err := resource.ValidatePowerRef(domain.PowerRef{Type: domain.PowerTypeMCP, Name: "other", Version: "v1.0.0"}); err == nil {
		t.Fatal("ValidatePowerRef() accepted a different name")
	}
	if err := resource.ValidatePowerRef(domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v1.0.1"}); err == nil {
		t.Fatal("ValidatePowerRef() accepted a different version")
	}
}
