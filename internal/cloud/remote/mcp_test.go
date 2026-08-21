package remote

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestGetMCPReturnsStrictValidatedDefinitionAndCleansOwnedDirectory(t *testing.T) {
	contents := []byte(`{"transport":"stdio","command":"npx","args":["-y","pkg"]}`)
	client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteMCPRef, contents)
	})
	root := t.TempDir()
	sentinel := filepath.Join(root, "preserve")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	client.mcpTempParent = root

	got, err := client.GetMCP(context.Background(), remoteMCPRef)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetMCP() = %#v, want %#v", got, want)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "preserve" {
		t.Fatalf("MCP temporary cleanup changed parent contents: %v", entries)
	}
}

func TestGetMCPRejectsUnknownDuplicateTrailingAndPlainHTTP(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"transport":"stdio","command":"npx","secret":"value"}`},
		{name: "duplicate field", body: `{"transport":"stdio","command":"first","command":"second"}`},
		{name: "trailing value", body: `{"transport":"stdio","command":"npx"} {}`},
		{name: "plain HTTP", body: `{"transport":"http","url":"http://mcp.example.com/mcp"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := []byte(tt.body)
			client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
				serveValidContent(t, w, r, remoteMCPRef, contents)
			})
			root := t.TempDir()
			client.mcpTempParent = root
			_, err := client.GetMCP(context.Background(), remoteMCPRef)
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("GetMCP() error = %v, want ErrInvalidResponse", err)
			}
			assertDirectoryEmpty(t, root)
		})
	}
}

func TestGetMCPRejectsSymlinkTemporaryParentWithoutDeletingTarget(t *testing.T) {
	contents := []byte(`{"transport":"stdio","command":"npx"}`)
	client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteMCPRef, contents)
	})
	target := t.TempDir()
	sentinel := filepath.Join(target, "preserve")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	client.mcpTempParent = link

	if _, err := client.GetMCP(context.Background(), remoteMCPRef); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP() error = %v, want ErrUnsafeContent", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("temporary parent target sentinel = %q, %v, want preserved", got, err)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "preserve") {
		t.Fatalf("temporary parent target changed: %v", entries)
	}
}
