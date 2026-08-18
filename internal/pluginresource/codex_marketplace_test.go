package pluginresource

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestCodexMarketplaceGeneratesSingleLocalPluginDocument(t *testing.T) {
	// 捕获错误变更：Codex marketplace 的身份、source 或安装策略偏离原生约束。
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", "demo-v0.1.0")
	if err := os.MkdirAll(filepath.Join(pluginPath, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "demo-v0.1.0",
  "version": "0.1.0",
  "description": "Demo tools",
  "author": {"name": "Example Author"},
  "interface": {"displayName": "Demo Display", "category": "Productivity"}
}`
	if err := os.WriteFile(filepath.Join(pluginPath, ".codex-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (codexMarketplaceBuilder{}).Build(domain.AgentCodex, ref, root, pluginPath); err != nil {
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
	if document.Name != "apm-demo" || document.Interface.DisplayName != "Demo Display" {
		t.Fatalf("marketplace identity = %q/%q, want apm-demo/Demo Display", document.Name, document.Interface.DisplayName)
	}
	if len(document.Plugins) != 1 {
		t.Fatalf("plugins count = %d, want 1", len(document.Plugins))
	}
	plugin := document.Plugins[0]
	if plugin.Name != "demo-v0.1.0" || plugin.Source.Source != "local" || plugin.Source.Path != "./plugins/demo-v0.1.0" {
		t.Fatalf("plugin identity/source = %#v, want demo-v0.1.0 local ./plugins/demo-v0.1.0", plugin)
	}
	if plugin.Policy.Installation != "AVAILABLE" || plugin.Policy.Authentication != "ON_INSTALL" || plugin.Category != "Productivity" {
		t.Fatalf("plugin policy/category = %#v/%q, want AVAILABLE ON_INSTALL/Productivity", plugin.Policy, plugin.Category)
	}
	if _, err := os.Lstat(filepath.Join(root, ".claude-plugin", "marketplace.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Claude marketplace stat error = %v, want not exist", err)
	}
}

func TestCodexMarketplaceUsesUnbrandedDisplayDefaults(t *testing.T) {
	// 捕获错误变更：缺失展示字段时输出空值或复制外部品牌默认值。
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", "demo-v0.1.0")
	if err := os.MkdirAll(filepath.Join(pluginPath, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginPath, ".codex-plugin", "plugin.json"), []byte(`{"name":"demo-v0.1.0","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (codexMarketplaceBuilder{}).Build(domain.AgentCodex, ref, root, pluginPath); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var document struct {
		Interface struct {
			DisplayName string `json:"displayName"`
		} `json:"interface"`
		Plugins []struct {
			Category string `json:"category"`
		} `json:"plugins"`
	}
	if err := decodeJSONFile(filepath.Join(root, ".agents", "plugins", "marketplace.json"), &document); err != nil {
		t.Fatal(err)
	}
	if document.Interface.DisplayName != "demo" || len(document.Plugins) != 1 || document.Plugins[0].Category != "Coding" {
		t.Fatalf("defaults = %q/%#v, want demo/Coding", document.Interface.DisplayName, document.Plugins)
	}
}
