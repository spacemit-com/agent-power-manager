package cloud_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalClientDownloadPluginToCopiesExactAgentPackage(t *testing.T) {
	// 捕获错误变更：忽略 Agent 维度或把 Cloud marketplace 一起复制到原始包。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	tests := []struct {
		agent        domain.AgentID
		manifestPath string
	}{
		{agent: domain.AgentCodex, manifestPath: filepath.Join(".codex-plugin", "plugin.json")},
		{agent: domain.AgentClaudeCode, manifestPath: filepath.Join(".claude-plugin", "plugin.json")},
		{agent: domain.AgentHermes, manifestPath: filepath.Join(".claude-plugin", "plugin.json")},
	}
	for _, test := range tests {
		t.Run(string(test.agent), func(t *testing.T) {
			destination := t.TempDir()
			if err := client.DownloadPluginTo(context.Background(), test.agent, ref, destination); err != nil {
				t.Fatalf("DownloadPluginTo() error = %v", err)
			}
			manifestData, err := os.ReadFile(filepath.Join(destination, test.manifestPath))
			if err != nil {
				t.Fatalf("read Agent manifest: %v", err)
			}
			var manifest struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal(manifestData, &manifest); err != nil {
				t.Fatalf("decode Agent manifest: %v", err)
			}
			if manifest.Name != "demo-plugin-v0.1.0" || manifest.Version != "0.1.0" {
				t.Fatalf("Agent manifest identity = %#v, want demo-plugin-v0.1.0@0.1.0", manifest)
			}
			if _, err := os.Stat(filepath.Join(destination, "skills", "demo-plugin", "SKILL.md")); err != nil {
				t.Fatalf("stat downloaded skill: %v", err)
			}
			assertTreeHasNoPluginMarketplace(t, destination)
		})
	}
}

func TestLocalClientDownloadPluginToReturnsExactMissesBeforeTouchingDestination(t *testing.T) {
	// 捕获错误变更：先拼接未校验的 Agent/ref 路径，或把精确 miss 暴露为文件系统错误。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		agent domain.AgentID
		ref   domain.PowerRef
	}{
		{name: "unsupported Agent", agent: "other", ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "missing logical name", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "missing", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "missing version", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v9.9.9"}},
		{name: "wrong Power type", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypeSkill, Version: "v0.1.0"}},
		{name: "escaping logical name", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "../demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}},
		{name: "escaping version", agent: domain.AgentCodex, ref: domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "../v0.1.0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "untouched")
			err := client.DownloadPluginTo(context.Background(), test.agent, test.ref, destination)
			if !errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("DownloadPluginTo() error = %v, want ErrNotFound", err)
			}
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination stat error = %v, want not exist", err)
			}
		})
	}
}

func TestLocalClientDownloadPluginToRejectsUnsafeDestinations(t *testing.T) {
	// 捕获错误变更：跟随软链接目标或覆盖调用方已有内容。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}

	nonEmpty := t.TempDir()
	sentinel := filepath.Join(nonEmpty, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadPluginTo(context.Background(), domain.AgentCodex, ref, nonEmpty); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("non-empty destination error = %v, want destination error", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("non-empty sentinel = %q, %v, want preserved", got, err)
	}

	symlinkTarget := t.TempDir()
	symlinkDestination := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(symlinkTarget, symlinkDestination); err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadPluginTo(context.Background(), domain.AgentCodex, ref, symlinkDestination); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("symlink destination error = %v, want destination error", err)
	}
	entries, err := os.ReadDir(symlinkTarget)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target entries = %v, %v, want empty", entries, err)
	}

	ancestorTarget := t.TempDir()
	ancestorLink := filepath.Join(t.TempDir(), "linked-parent")
	if err := os.Symlink(ancestorTarget, ancestorLink); err != nil {
		t.Fatal(err)
	}
	throughAncestor := filepath.Join(ancestorLink, "plugin")
	if err := client.DownloadPluginTo(context.Background(), domain.AgentCodex, ref, throughAncestor); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("symlink ancestor destination error = %v, want destination error", err)
	}
	entries, err = os.ReadDir(ancestorTarget)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink ancestor target entries = %v, %v, want empty", entries, err)
	}
}

func TestLocalClientDownloadPluginToAllowsCallerControlledRelativeDestination(t *testing.T) {
	// 捕获错误变更：将“调用方控制”额外收窄为只允许绝对路径。
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	working := t.TempDir()
	t.Chdir(working)
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := client.DownloadPluginTo(context.Background(), domain.AgentCodex, ref, "plugin"); err != nil {
		t.Fatalf("DownloadPluginTo() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(working, "plugin", ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("stat relative destination manifest: %v", err)
	}
}

func TestLocalClientDownloadPluginToDoesNotCreateCloudOwnedCacheOrStaging(t *testing.T) {
	// 捕获错误变更：新下载边界复用旧 DownloadPlugin 并自行写入 cache/staging。
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := client.DownloadPluginTo(context.Background(), domain.AgentCodex, ref, filepath.Join(t.TempDir(), "plugin")); err != nil {
		t.Fatalf("DownloadPluginTo() error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(cacheHome, "apm", "plugins"),
		filepath.Join(cacheHome, "apm", ".tmp", "plugins"),
		filepath.Join(stateHome, ".apm", "staging"),
		filepath.Join(stateHome, ".apm", ".tmp", "staging"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Cloud-owned path %q stat error = %v, want not exist", path, err)
		}
	}
}

func assertTreeHasNoPluginMarketplace(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == filepath.Join(".agents", "plugins", "marketplace.json") ||
			relative == filepath.Join(".claude-plugin", "marketplace.json") {
			t.Errorf("downloaded tree contains marketplace %q", relative)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk downloaded tree: %v", err)
	}
}

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
