package pluginresource

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestClaudeMarketplaceGeneratesSingleLocalPluginDocument(t *testing.T) {
	// 捕获错误变更：Claude marketplace 未保留原生作者、版本、描述或本地 source。
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", "demo-v0.1.0")
	if err := os.MkdirAll(filepath.Join(pluginPath, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "demo-v0.1.0",
  "version": "0.1.0",
  "description": "Demo tools",
  "author": {"name": "Example Author"}
}`
	if err := os.WriteFile(filepath.Join(pluginPath, ".claude-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (claudeMarketplaceBuilder{}).Build(domain.AgentClaudeCode, ref, root, pluginPath); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Metadata struct {
			Description string `json:"description"`
			Version     string `json:"version"`
		} `json:"metadata"`
		Plugins []struct {
			Name        string `json:"name"`
			Source      string `json:"source"`
			Description string `json:"description"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Name != "apm-demo" || document.Owner.Name != "Example Author" {
		t.Fatalf("marketplace identity/owner = %q/%q, want apm-demo/Example Author", document.Name, document.Owner.Name)
	}
	if document.Metadata.Version != "0.1.0" || document.Metadata.Description != "Demo tools" {
		t.Fatalf("metadata = %#v, want 0.1.0/Demo tools", document.Metadata)
	}
	if len(document.Plugins) != 1 {
		t.Fatalf("plugins count = %d, want 1", len(document.Plugins))
	}
	plugin := document.Plugins[0]
	if plugin.Name != "demo-v0.1.0" || plugin.Source != "./plugins/demo-v0.1.0" || plugin.Description != "Demo tools" {
		t.Fatalf("plugin = %#v, want demo-v0.1.0 ./plugins/demo-v0.1.0/Demo tools", plugin)
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents", "plugins", "marketplace.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex marketplace stat error = %v, want not exist", err)
	}
}

func TestClaudeMarketplaceUsesExactAPMDefaults(t *testing.T) {
	// 捕获错误变更：缺失作者或描述时输出空值或非 APM 约定默认值。
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", "demo-v0.1.0")
	if err := os.MkdirAll(filepath.Join(pluginPath, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginPath, ".claude-plugin", "plugin.json"), []byte(`{"name":"demo-v0.1.0","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (claudeMarketplaceBuilder{}).Build(domain.AgentClaudeCode, ref, root, pluginPath); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var document struct {
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Metadata struct {
			Description string `json:"description"`
		} `json:"metadata"`
		Plugins []struct {
			Description string `json:"description"`
		} `json:"plugins"`
	}
	if err := decodeJSONFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), &document); err != nil {
		t.Fatal(err)
	}
	wantDescription := "APM local marketplace for demo"
	if document.Owner.Name != "APM" || document.Metadata.Description != wantDescription || len(document.Plugins) != 1 || document.Plugins[0].Description != wantDescription {
		t.Fatalf("defaults = %q/%q/%#v, want APM/%q", document.Owner.Name, document.Metadata.Description, document.Plugins, wantDescription)
	}
}
