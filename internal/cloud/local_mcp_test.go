package cloud_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalClientListsMCPWithSkillsInStableOrder(t *testing.T) {
	root := t.TempDir()
	writeCloudMarker(t, filepath.Join(root, "skills", "zeta", "v1.0.0", "SKILL.md"), "skill")
	writeCloudMarker(t, filepath.Join(root, "mcps", "alpha", "v2.0.0", "MCP.json"), `{"transport":"http","url":"https://example.com/mcp"}`)
	writeCloudMarker(t, filepath.Join(root, "mcps", "alpha", "v1.0.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	client := newMCPTestClient(t, root)

	got, err := client.List()
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
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := client.GetMCP(domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v1.2.3"})
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
		{name: "trailing value", body: `{"transport":"stdio","command":"npx"} {}`},
		{name: "invalid union", body: `{"transport":"http","url":"https://example.com","command":"npx"}`},
		{name: "wrong type", body: `{"transport":"stdio","command":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeCloudMarker(t, filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json"), tt.body)
			client := newMCPTestClient(t, root)
			if _, err := client.GetMCP(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}); err == nil || errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("GetMCP() error = %v, want definition error", err)
			}
		})
	}
}

func TestLocalClientGetMCPMapsUnsupportedReferencesAndSymlinkMarkerToNotFound(t *testing.T) {
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
		{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"},
	} {
		if _, err := client.GetMCP(ref); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("GetMCP(%#v) error = %v, want ErrNotFound", ref, err)
		}
	}
}

func newMCPTestClient(t *testing.T, root string) *cloud.LocalClient {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(root)
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
