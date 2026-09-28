package pluginresource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

// TestDerivedAgentManifestsCarryNoLocalizedFields 守护派生边界：
// name_zh/name_en/description_zh 只属于 canonical apc-plugin.json，APM 派生出的
// Codex/Claude/Hermes/Bibit 原生清单不得携带它们（ADR-038/ADR-039）。
// 这些原生清单被 reflect.DeepEqual 严格复验，一旦带上新字段就会破坏复验。
func TestDerivedAgentManifestsCarryNoLocalizedFields(t *testing.T) {
	ref := pluginartifactTestRef()
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", ref.Name)
	writeCanonicalPluginPackageV2(t, pluginPath, ref)

	canonical, err := os.ReadFile(filepath.Join(pluginPath, "apc-plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), "name_zh") || !strings.Contains(string(canonical), "name_en") ||
		!strings.Contains(string(canonical), "description_zh") {
		t.Fatalf("canonical apc-plugin.json = %s, want localized fields preserved", canonical)
	}

	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAgentLandings(root, pluginPath, manifest); err != nil {
		t.Fatalf("writeAgentLandings() error = %v", err)
	}

	for _, agent := range pluginartifact.PluginLandingAgents() {
		landing, err := pluginartifact.LandingPluginPath(root, agent, ref.Name)
		if err != nil {
			t.Fatal(err)
		}
		err = filepath.WalkDir(landing, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(contents), "name_zh") || strings.Contains(string(contents), "name_en") ||
				strings.Contains(string(contents), "description_zh") {
				t.Errorf("derived %s file %q carries localized display fields: %s", agent, path, contents)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestDerivedAgentManifestsStillDeriveForV1Artifact 守护安装侧兼容：
// v1 canonical 包（无展示字段）仍可加载并派生各 Agent 落盘清单，存量资源不受提交侧门禁影响。
func TestDerivedAgentManifestsStillDeriveForV1Artifact(t *testing.T) {
	ref := pluginartifactTestRef()
	root := t.TempDir()
	pluginPath := filepath.Join(root, "plugins", ref.Name)
	writeCanonicalPluginPackage(t, pluginPath, ref, "Demo")

	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		t.Fatalf("LoadAPCPluginManifest(v1) error = %v, want nil", err)
	}
	if manifest.SchemaVersion != domain.PowerSchemaVersionV1 {
		t.Fatalf("loaded schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if err := writeAgentLandings(root, pluginPath, manifest); err != nil {
		t.Fatalf("writeAgentLandings(v1) error = %v, want nil", err)
	}
	for _, agent := range pluginartifact.PluginLandingAgents() {
		landing, err := pluginartifact.LandingPluginPath(root, agent, ref.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(landing); err != nil {
			t.Fatalf("%s landing for v1 artifact = %v, want a derived directory", agent, err)
		}
	}
}
