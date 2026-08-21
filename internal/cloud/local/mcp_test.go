package local_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/domain"
)

func TestLocalClientListsMCPWithSkillsInStableOrder(t *testing.T) {
	root := t.TempDir()
	writeCloudMarker(t, filepath.Join(root, "skills", "zeta", "v1.0.0", "SKILL.md"), "skill")
	writeCloudMarker(t, filepath.Join(root, "mcps", "alpha", "v2.0.0", "MCP.json"), `{"transport":"http","url":"https://example.com/mcp"}`)
	writeCloudMarker(t, filepath.Join(root, "mcps", "alpha", "v1.0.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	client := newMCPTestClient(t, root)

	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "alpha", Type: domain.PowerTypeMCP, Version: "v1.0.0"},
		{Name: "alpha", Type: domain.PowerTypeMCP, Version: "v2.0.0"},
		{Name: "zeta", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientGetMCPReadsExactVersionWithoutCacheMaterialization(t *testing.T) {
	root := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeCloudMarker(t, filepath.Join(root, "mcps", "filesystem", "v1.2.3", "MCP.json"), `{"transport":"stdio","command":"npx","args":["-y","pkg"]}`)
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.GetMCP(context.Background(), domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetMCP() = %#v, want %#v", got, want)
	}
	entries, err := os.ReadDir(cacheHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("GetMCP() materialized cache entries: %v", entries)
	}
}

func TestLocalClientGetMCPRejectsInvalidResourceDefinitions(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"transport":"stdio","command":"npx","secret":"x"}`},
		{name: "duplicate root field", body: `{"transport":"stdio","command":"first","command":"second"}`},
		{name: "trailing value", body: `{"transport":"stdio","command":"npx"} {}`},
		{name: "invalid union", body: `{"transport":"http","url":"https://example.com","command":"npx"}`},
		{name: "stdio forbidden null URL", body: `{"transport":"stdio","command":"npx","url":null}`},
		{name: "http forbidden empty command", body: `{"transport":"http","url":"https://example.com","command":""}`},
		{name: "http forbidden empty args", body: `{"transport":"http","url":"https://example.com","args":[]}`},
		{name: "plain HTTP endpoint", body: `{"transport":"http","url":"http://example.com/mcp"}`},
		{name: "wrong type", body: `{"transport":"stdio","command":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeCloudMarker(t, filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json"), tt.body)
			client := newMCPTestClient(t, root)
			if _, err := client.GetMCP(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}); err == nil || errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("GetMCP() error = %v, want definition error", err)
			}
		})
	}
}

func TestLocalClientGetMCPMapsUnsupportedReferencesToNotFoundAndSymlinkToUnsafeContent(t *testing.T) {
	root := t.TempDir()
	realMarker := filepath.Join(root, "real.json")
	writeCloudMarker(t, realMarker, `{"transport":"stdio","command":"npx"}`)
	marker := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realMarker, marker); err != nil {
		t.Fatal(err)
	}
	client := newMCPTestClient(t, root)

	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		{Name: "missing", Type: domain.PowerTypeMCP, Version: "v1.0.0"},
	} {
		if _, err := client.GetMCP(context.Background(), ref); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("GetMCP(%#v) error = %v, want ErrNotFound", ref, err)
		}
	}
	if _, err := client.GetMCP(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP(symlink) error = %v, want ErrUnsafeContent", err)
	}
}

func TestLocalClientResolveReturnsLatestOrExact(t *testing.T) {
	client := newMCPTestClient(t, writeTwoVersionRoot(t))

	latest, err := client.Resolve(context.Background(), domain.PowerRef{
		Type: domain.PowerTypeMCP, Name: "filesystem",
	})
	if err != nil || latest.Version != "v0.2.0" {
		t.Fatalf("Resolve latest = %#v, %v", latest, err)
	}
	exact, err := client.Resolve(context.Background(), domain.PowerRef{
		Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.1.0",
	})
	if err != nil || exact.Version != "v0.1.0" {
		t.Fatalf("Resolve exact = %#v, %v", exact, err)
	}
}

func TestLocalClientListSortsVersionsBySemVer(t *testing.T) {
	root := t.TempDir()
	writeCloudMarker(t, filepath.Join(root, "mcps", "filesystem", "v0.10.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	writeCloudMarker(t, filepath.Join(root, "mcps", "filesystem", "v0.2.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	client := newMCPTestClient(t, root)

	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.2.0"},
		{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v0.10.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListReturnsCanceledContext(t *testing.T) {
	client := newMCPTestClient(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List() error = %v, want context.Canceled", err)
	}
}

func TestLocalClientGetMCPReturnsCanceledContext(t *testing.T) {
	client := newMCPTestClient(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.GetMCP(ctx, domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.1.0"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMCP() error = %v, want context.Canceled", err)
	}
}

func writeTwoVersionRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCloudMarker(t, filepath.Join(root, "mcps", "filesystem", "v0.1.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	writeCloudMarker(t, filepath.Join(root, "mcps", "filesystem", "v0.2.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	return root
}

func newMCPTestClient(t *testing.T, root string) *local.Client {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeCloudMarker(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
