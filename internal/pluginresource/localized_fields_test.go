package pluginresource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

// TestDerivedAgentManifestsCarryNoLocalizedFields 守护派生清单边界：
// name_zh/name_en/description_zh 只属于 canonical apc-plugin.json 与 canonical Skill 内容，
// APM 为 Codex/Claude/Hermes/Bibit 派生的**原生清单**不得携带它们（ADR-038 第 6 条、ADR-040）。
// 这些原生清单被 reflect.DeepEqual 严格复验，一旦带上新字段就会破坏复验。
//
// canonical Skill 子树是原样复制的内容，不是派生结果：SKILL.md 里的展示字段必须保留，
// 因此本测试逐文件区分「派生清单」与「复制的 canonical 内容」。
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

	localizedFields := []string{"name_zh", "name_en", "description_zh"}
	for _, agent := range pluginartifact.PluginLandingAgents() {
		landing, err := pluginartifact.LandingPluginPath(root, agent, ref.Name)
		if err != nil {
			t.Fatal(err)
		}
		for _, relative := range derivedManifestPaths(agent) {
			path := filepath.Join(landing, filepath.FromSlash(relative))
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read derived %s manifest %q: %v", agent, path, err)
			}
			for _, field := range localizedFields {
				if strings.Contains(string(contents), field) {
					t.Errorf("derived %s manifest %q carries localized display field %q: %s", agent, path, field, contents)
				}
			}
		}

		// 复制的 canonical Skill 内容必须保留展示字段，否则安装后的 Skill 会丢失本地化信息。
		skillPath := filepath.Join(landing, "skills", ref.Name, "SKILL.md")
		skillContents, err := os.ReadFile(skillPath)
		if err != nil {
			t.Fatalf("read copied canonical Skill %q: %v", skillPath, err)
		}
		if !strings.Contains(string(skillContents), "name_zh") {
			t.Errorf("copied canonical Skill %q lost localized display fields: %s", skillPath, skillContents)
		}
	}
}

// derivedManifestPaths 返回某个 Agent 落盘叶子中由 APM 派生的原生清单相对路径。
func derivedManifestPaths(agent domain.AgentID) []string {
	switch agent {
	case domain.AgentCodex:
		return []string{codexManifestPath}
	case domain.AgentClaudeCode:
		return []string{claudeManifestPath}
	case domain.AgentHermes, domain.AgentBibit:
		return []string{pluginManifestPath}
	default:
		return nil
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
