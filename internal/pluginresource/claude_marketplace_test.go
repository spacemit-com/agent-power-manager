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

func TestClaudeMarketplaceGeneratesSingleLocalPluginDocument(t *testing.T) {
	// 捕获错误变更：Claude marketplace 未保留原生作者、版本、描述或本地 source。
	root := t.TempDir()
	pluginPath, err := pluginartifact.LandingPluginPath(root, domain.AgentClaudeCode, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginPath, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "demo",
  "version": "0.1.0",
  "description": "Demo tools",
  "skills": ["./skills/"]
}`
	if err := os.WriteFile(filepath.Join(pluginPath, ".claude-plugin", "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (claudeMarketplaceBuilder{}).Build(ref, root); err != nil {
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
	if document.Name != "apm-demo" || document.Owner.Name != "APM" {
		t.Fatalf("marketplace identity/owner = %q/%q, want apm-demo/APM", document.Name, document.Owner.Name)
	}
	if document.Metadata.Version != "0.1.0" || document.Metadata.Description != "Demo tools" {
		t.Fatalf("metadata = %#v, want 0.1.0/Demo tools", document.Metadata)
	}
	if len(document.Plugins) != 1 {
		t.Fatalf("plugins count = %d, want 1", len(document.Plugins))
	}
	plugin := document.Plugins[0]
	if plugin.Name != "demo" || plugin.Source != "./landing/claude-code/plugins/demo" || plugin.Description != "Demo tools" {
		t.Fatalf("plugin = %#v, want demo ./landing/claude-code/plugins/demo/Demo tools", plugin)
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents", "plugins", "marketplace.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex marketplace stat error = %v, want not exist", err)
	}
}

func TestClaudeMarketplaceRejectsIncompleteGeneratedManifest(t *testing.T) {
	// 捕获错误变更：builder 接纳缺失 canonical description/skills 的 schema 漂移文件。
	root := t.TempDir()
	pluginPath, err := pluginartifact.LandingPluginPath(root, domain.AgentClaudeCode, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pluginPath, ".claude-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginPath, ".claude-plugin", "plugin.json"), []byte(`{"name":"demo","version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
	if err := (claudeMarketplaceBuilder{}).Build(ref, root); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Build() error = %v, want ErrInvalidArtifact", err)
	}
}
