package pluginresource

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

func TestCodexMarketplaceGeneratesSingleLocalPluginDocument(t *testing.T) {
	// 捕获错误变更：Codex marketplace 的身份、source 或安装策略偏离原生约束。
	root := t.TempDir()
	pluginPath, err := pluginartifact.LandingPluginPath(root, domain.AgentCodex, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginPath, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "demo",
  "version": "0.1.0",
  "description": "Demo tools",
  "skills": "./skills/"
}`
	if err := os.WriteFile(filepath.Join(pluginPath, ".codex-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (codexMarketplaceBuilder{}).Build(ref, root); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".agents", "plugins", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Name      string `json:"name"`
		Interface struct {
			DisplayName string `json:"displayName"`
		} `json:"interface"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
			Policy struct {
				Installation   string `json:"installation"`
				Authentication string `json:"authentication"`
			} `json:"policy"`
			Category string `json:"category"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Name != "apm-demo" || document.Interface.DisplayName != "demo" {
		t.Fatalf("marketplace identity = %q/%q, want apm-demo/demo", document.Name, document.Interface.DisplayName)
	}
	if len(document.Plugins) != 1 {
		t.Fatalf("plugins count = %d, want 1", len(document.Plugins))
	}
	plugin := document.Plugins[0]
	if plugin.Name != "demo" || plugin.Source.Source != "local" || plugin.Source.Path != "./landing/codex/plugins/demo" {
		t.Fatalf("plugin identity/source = %#v, want demo local ./landing/codex/plugins/demo", plugin)
	}
	if plugin.Policy.Installation != "AVAILABLE" || plugin.Policy.Authentication != "ON_INSTALL" || plugin.Category != "Coding" {
		t.Fatalf("plugin policy/category = %#v/%q, want AVAILABLE ON_INSTALL/Coding", plugin.Policy, plugin.Category)
	}
	if _, err := os.Lstat(filepath.Join(root, ".claude-plugin", "marketplace.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Claude marketplace stat error = %v, want not exist", err)
	}
}

func TestCodexMarketplaceRejectsIncompleteGeneratedManifest(t *testing.T) {
	// 捕获错误变更：builder 接纳缺失 canonical description/skills 的 schema 漂移文件。
	root := t.TempDir()
	pluginPath, err := pluginartifact.LandingPluginPath(root, domain.AgentCodex, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginPath, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginPath, ".codex-plugin", "plugin.json"), []byte(`{"name":"demo","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (codexMarketplaceBuilder{}).Build(ref, root); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Build() error = %v, want ErrInvalidArtifact", err)
	}
}
