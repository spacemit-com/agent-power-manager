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

func TestLocalClientListsPluginsFromClaudeMarketplaceOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	writePluginMarketplace(t, root, `{
  "name": "apm",
  "plugins": [
    {"name":"demo-plugin-v0.1.0","version":"0.1.0","source":"./plugins/demo-plugin-v0.1.0"},
    {"name":"demo-plugin-v0.0.1","version":"0.0.1","source":"./plugins/demo-plugin-v0.0.1"}
  ]
}`)
	writePluginSource(t, root, "demo-plugin-v0.0.1")
	writePluginSource(t, root, "demo-plugin-v0.1.0")
	codex := filepath.Join(root, "plugins", ".agents", "plugins")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "marketplace.json"), []byte(`{"plugins":[{"name":"ignored-v9.9.9","version":"9.9.9"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"},
		{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientGetPluginReturnsActualNameAndMarketplace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	writePluginMarketplace(t, root, `{
  "name": "apm",
  "plugins": [
    {"name":"demo-plugin-v0.0.1","version":"0.0.1","source":"./plugins/demo-plugin-v0.0.1"}
  ]
}`)
	writePluginSource(t, root, "demo-plugin-v0.0.1")
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.GetPlugin(domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.PluginDefinition{
		ActualName: "demo-plugin-v0.0.1",
		Marketplace: domain.MarketplaceDefinition{
			Name:   "apm",
			Source: filepath.Join(root, "plugins"),
		},
	}
	if got != want {
		t.Fatalf("GetPlugin() = %#v, want %#v", got, want)
	}
}

func TestLocalClientPluginAuthorityFailures(t *testing.T) {
	tests := []struct {
		name        string
		marketplace string
		prepare     func(*testing.T, string)
	}{
		{name: "malformed JSON", marketplace: `{`},
		{name: "trailing JSON", marketplace: `{"name":"apm","plugins":[]} {}`},
		{name: "invalid native version", marketplace: `{"name":"apm","plugins":[{"name":"demo-v1.0.0","version":"v1.0.0","source":"./plugins/demo-v1.0.0"}]}`},
		{name: "mismatched name version", marketplace: `{"name":"apm","plugins":[{"name":"demo-v1.0.1","version":"1.0.0","source":"./plugins/demo-v1.0.1"}]}`},
		{name: "duplicate logical ref", marketplace: `{"name":"apm","plugins":[{"name":"demo-v1.0.0","version":"1.0.0","source":"./plugins/one"},{"name":"demo-v1.0.0","version":"1.0.0","source":"./plugins/two"}]}`},
		{name: "escaping source", marketplace: `{"name":"apm","plugins":[{"name":"demo-v1.0.0","version":"1.0.0","source":"../outside"}]}`},
		{
			name:        "symlink source",
			marketplace: `{"name":"apm","plugins":[{"name":"demo-v1.0.0","version":"1.0.0","source":"./plugins/demo-v1.0.0"}]}`,
			prepare: func(t *testing.T, root string) {
				t.Helper()
				outside := t.TempDir()
				path := filepath.Join(root, "plugins", "plugins")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(path, "demo-v1.0.0")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			writePluginMarketplace(t, root, test.marketplace)
			if test.prepare != nil {
				test.prepare(t, root)
			}
			client, err := cloud.NewLocalClient(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.List(); err == nil {
				t.Fatal("List() error = nil")
			}
		})
	}
}

func TestLocalClientPluginMissingIndexAndExactRef(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil || len(got) != 0 {
		t.Fatalf("List() = %#v, %v", got, err)
	}
	if _, err := client.GetPlugin(domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}); !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("GetPlugin() error = %v, want ErrNotFound", err)
	}
}

func writePluginMarketplace(t *testing.T, root, contents string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePluginSource(t *testing.T, root, actualName string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", "plugins", actualName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(actualName), 0o644); err != nil {
		t.Fatal(err)
	}
}
