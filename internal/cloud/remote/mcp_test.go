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
	root := trustedTestDir(t)
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

func TestGetMCPRejectsInjectedTemporaryParentThroughSymlinkedAncestor(t *testing.T) {
	contents := []byte(`{"transport":"stdio","command":"npx"}`)
	var requests int
	client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests++
		serveValidContent(t, w, r, remoteMCPRef, contents)
	})
	physicalRoot := trustedTestDir(t)
	physicalParent := filepath.Join(physicalRoot, "private", "tmp")
	if err := os.MkdirAll(physicalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(trustedTestDir(t), "var")
	if err := os.Symlink(filepath.Join(physicalRoot, "private"), aliasRoot); err != nil {
		t.Fatal(err)
	}
	client.mcpTempParent = filepath.Join(aliasRoot, "tmp")

	if _, err := client.GetMCP(context.Background(), remoteMCPRef); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP() error = %v, want symlink ancestor ErrUnsafeContent", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want injected symlink ancestor rejected before network", requests)
	}
	assertDirectoryEmpty(t, physicalParent)
}

func TestGetMCPCanonicalizesSymlinkedSystemTempDirectory(t *testing.T) {
	contents := []byte(`{"transport":"stdio","command":"npx"}`)
	client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteMCPRef, contents)
	})
	physicalParent := t.TempDir()
	if err := os.Chmod(physicalParent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "tmp-alias")
	if err := os.Symlink(physicalParent, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", alias)

	if _, err := client.GetMCP(context.Background(), remoteMCPRef); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, physicalParent)
}

func TestGetMCPCleanupDetectsOwnedChildIdentityCorruption(t *testing.T) {
	contents := []byte(`{"transport":"stdio","command":"npx"}`)
	client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteMCPRef, contents)
	})
	root := trustedTestDir(t)
	client.mcpTempParent = root
	var replacement string
	client.beforeMCPCleanup = func(owned *ownedDirectory) {
		replacement = filepath.Join(root, owned.name)
		if err := os.Rename(replacement, replacement+".owned-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(replacement, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(replacement, "preserve"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err := client.GetMCP(context.Background(), remoteMCPRef)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP() error = %v, want cleanup substitution ErrUnsafeContent", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(replacement, "preserve")); readErr != nil || string(got) != "keep" {
		t.Fatalf("substituted MCP child = %q, %v, want preserved", got, readErr)
	}
}

func TestGetMCPRejectsStrictJSONInvalidUnionAndPlainHTTP(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"transport":"stdio","command":"npx","secret":"value"}`},
		{name: "duplicate field", body: `{"transport":"stdio","command":"first","command":"second"}`},
		{name: "case-fold duplicate field", body: `{"transport":"stdio","command":"first","COMMAND":"second"}`},
		{name: "trailing value", body: `{"transport":"stdio","command":"npx"} {}`},
		{name: "null transport", body: `{"transport":null,"command":"npx"}`},
		{name: "stdio null command", body: `{"transport":"stdio","command":null}`},
		{name: "stdio null args", body: `{"transport":"stdio","command":"npx","args":null}`},
		{name: "stdio forbidden null URL", body: `{"transport":"stdio","command":"npx","url":null}`},
		{name: "http null URL", body: `{"transport":"http","url":null}`},
		{name: "http forbidden empty command", body: `{"transport":"http","url":"https://mcp.example.com/mcp","command":""}`},
		{name: "http forbidden empty args", body: `{"transport":"http","url":"https://mcp.example.com/mcp","args":[]}`},
		{name: "plain HTTP", body: `{"transport":"http","url":"http://mcp.example.com/mcp"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := []byte(tt.body)
			client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
				serveValidContent(t, w, r, remoteMCPRef, contents)
			})
			root := trustedTestDir(t)
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
	target := trustedTestDir(t)
	sentinel := filepath.Join(target, "preserve")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(trustedTestDir(t), "linked")
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

func TestGetMCPRejectsWritableInjectedTemporaryParentWithoutRequest(t *testing.T) {
	contents := []byte(`{"transport":"stdio","command":"npx"}`)
	requests := 0
	client := newContentClient(t, remoteMCPRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests++
		serveValidContent(t, w, r, remoteMCPRef, contents)
	})
	root := trustedTestDir(t)
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	client.mcpTempParent = root

	if _, err := client.GetMCP(context.Background(), remoteMCPRef); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP() error = %v, want directory trust ErrUnsafeContent", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want untrusted parent rejected before network", requests)
	}
}
