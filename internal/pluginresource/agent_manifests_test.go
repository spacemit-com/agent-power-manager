package pluginresource

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"go.yaml.in/yaml/v3"
)

func TestAgentLandingGenerationIsDeterministicAndUsesExactSchemas(t *testing.T) {
	// 捕获错误变更：同一 canonical index 生成不同字节、错误版本格式或 Agent schema 字段漂移。
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}
	description := "Quoted \"description\"\nvalue: true"
	first := filepath.Join(t.TempDir(), "plugins", ref.Name)
	second := filepath.Join(t.TempDir(), "plugins", ref.Name)
	for _, root := range []string{first, second} {
		writeCanonicalPluginPackage(t, root, ref, description)
		manifest, err := pluginartifact.LoadAPCPluginManifest(ref, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeAgentLandings(filepath.Dir(filepath.Dir(root)), root, manifest); err != nil {
			t.Fatalf("writeAgentLandings() error = %v", err)
		}
	}

	landings := map[domain.AgentID]string{
		domain.AgentCodex:      codexManifestPath,
		domain.AgentClaudeCode: claudeManifestPath,
		domain.AgentHermes:     pluginManifestPath,
		domain.AgentBibit:      pluginManifestPath,
	}
	for agent, relative := range landings {
		firstPath := landingFilePath(t, filepath.Dir(filepath.Dir(first)), agent, ref.Name, relative)
		secondPath := landingFilePath(t, filepath.Dir(filepath.Dir(second)), agent, ref.Name, relative)
		firstBytes, err := os.ReadFile(firstPath)
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := os.ReadFile(secondPath)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(firstBytes, secondBytes) {
			t.Fatalf("generated %s %q differs across roots\nfirst: %q\nsecond: %q", agent, relative, firstBytes, secondBytes)
		}
		info, err := os.Stat(firstPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("generated %s %q mode = %o, want 0600", agent, relative, info.Mode().Perm())
		}
	}

	assertGeneratedJSONSchema(t, landingFilePath(t, filepath.Dir(filepath.Dir(first)), domain.AgentCodex, ref.Name, codexManifestPath), map[string]any{
		"name":        "demo",
		"version":     "1.2.3",
		"description": description,
		"skills":      "./skills/",
	})
	assertGeneratedJSONSchema(t, landingFilePath(t, filepath.Dir(filepath.Dir(first)), domain.AgentClaudeCode, ref.Name, claudeManifestPath), map[string]any{
		"name":        "demo",
		"version":     "1.2.3",
		"description": description,
		"skills":      []any{"./skills/"},
	})

	// plugin.yaml 必须逐字节稳定：schema_version 首字段、schema 精确、无 author，且 Hermes 与 Bibit 共用同一内容。
	wantPluginYAML := "schema_version: 1\nname: demo\nversion: 1.2.3\ndescription: |-\n  Quoted \"description\"\n  value: true\n"
	for _, agent := range []domain.AgentID{domain.AgentHermes, domain.AgentBibit} {
		pluginYAMLBytes, err := os.ReadFile(landingFilePath(t, filepath.Dir(filepath.Dir(first)), agent, ref.Name, pluginManifestPath))
		if err != nil {
			t.Fatal(err)
		}
		if got := string(pluginYAMLBytes); got != wantPluginYAML {
			t.Fatalf("%s plugin.yaml = %q, want %q", agent, got, wantPluginYAML)
		}
		var pluginYAML map[string]any
		if err := yaml.Unmarshal(pluginYAMLBytes, &pluginYAML); err != nil {
			t.Fatal(err)
		}
		wantParsed := map[string]any{
			"schema_version": 1,
			"name":           "demo",
			"version":        "1.2.3",
			"description":    description,
		}
		if !reflect.DeepEqual(pluginYAML, wantParsed) {
			t.Fatalf("%s filesystem Plugin manifest = %#v, want %#v", agent, pluginYAML, wantParsed)
		}
	}

	// 单行描述保持四行可读形态，确认 author 等额外键绝不出现。
	plainRoot := t.TempDir()
	plainPlugin := filepath.Join(plainRoot, "plugins", ref.Name)
	writeCanonicalPluginPackage(t, plainPlugin, ref, "Demo tools")
	plainManifest, err := pluginartifact.LoadAPCPluginManifest(ref, plainPlugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAgentLandings(plainRoot, plainPlugin, plainManifest); err != nil {
		t.Fatalf("writeAgentLandings() error = %v", err)
	}
	for _, agent := range []domain.AgentID{domain.AgentHermes, domain.AgentBibit} {
		pluginYAMLBytes, err := os.ReadFile(landingFilePath(t, plainRoot, agent, ref.Name, pluginManifestPath))
		if err != nil {
			t.Fatal(err)
		}
		wantPlain := "schema_version: 1\nname: demo\nversion: 1.2.3\ndescription: Demo tools\n"
		if got := string(pluginYAMLBytes); got != wantPlain {
			t.Fatalf("%s plugin.yaml = %q, want %q", agent, got, wantPlain)
		}
	}

	initBytes, err := os.ReadFile(landingFilePath(t, filepath.Dir(filepath.Dir(first)), domain.AgentHermes, ref.Name, pluginInitPath))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(initBytes), "def register(ctx):\n    return None\n"; got != want {
		t.Fatalf("Hermes __init__.py = %q, want %q", got, want)
	}
}

func TestAgentLandingGenerationKeepsEachAgentOwnFilesOnly(t *testing.T) {
	// 捕获错误变更：某 Agent 的落盘叶子混入其他 Agent 的配置文件，安装目录将出现无关配置。
	root := t.TempDir()
	ref := pluginartifactTestRef()
	pluginPath := filepath.Join(root, "plugins", ref.Name)
	writeCanonicalPluginPackage(t, pluginPath, ref, "Demo")
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
		want := []string{"skills/demo/SKILL.md", "skills/demo/skill-card.md"}
		switch agent {
		case domain.AgentCodex:
			want = append(want, ".codex-plugin/plugin.json")
		case domain.AgentClaudeCode:
			want = append(want, ".claude-plugin/plugin.json")
		case domain.AgentHermes:
			want = append(want, "__init__.py", "plugin.yaml")
		case domain.AgentBibit:
			want = append(want, "plugin.yaml")
		}
		sort.Strings(want)
		if got := relativeFiles(t, landing); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s landing files = %v, want %v", agent, got, want)
		}
	}

	// canonical 包保持原样：派生内容绝不回写到 canonical 叶子。
	canonicalFiles := relativeFiles(t, pluginPath)
	wantCanonical := []string{"apc-plugin.json", "skills/demo/SKILL.md", "skills/demo/skill-card.md"}
	sort.Strings(wantCanonical)
	if !reflect.DeepEqual(canonicalFiles, wantCanonical) {
		t.Fatalf("canonical package files = %v, want %v", canonicalFiles, wantCanonical)
	}
}

func TestAgentLandingGenerationDoesNotOverwriteExistingDerivedFile(t *testing.T) {
	// 捕获错误变更：生成器覆盖已存在节点，使 raw 校验与不可变临时树边界失去意义。
	root := t.TempDir()
	ref := pluginartifactTestRef()
	pluginPath := filepath.Join(root, "plugins", ref.Name)
	writeCanonicalPluginPackage(t, pluginPath, ref, "Demo")
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := pluginartifact.LandingPluginPath(root, domain.AgentCodex, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(target, codexManifestPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentLandings(root, pluginPath, manifest); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("writeAgentLandings() error = %v, want ErrInvalidArtifact", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "existing" {
		t.Fatalf("existing target = %q, want unchanged", contents)
	}
}

func TestAgentLandingPartialGenerationCannotValidateSharedArtifact(t *testing.T) {
	// 捕获错误变更：部分 Agent 落盘后，后续写失败仍被共享 validator 当作可发布完整 artifact。
	root := t.TempDir()
	ref := pluginartifactTestRef()
	pluginPath := filepath.Join(root, "plugins", ref.Name)
	writeCanonicalPluginPackage(t, pluginPath, ref, "Demo")
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := pluginartifact.LandingPluginPath(root, domain.AgentHermes, ref.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, pluginManifestPath), []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentLandings(root, pluginPath, manifest); err == nil {
		t.Fatal("writeAgentLandings() error = nil, want blocked Hermes generation")
	}
	if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
		t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
	}
}

func TestAgentLandingPartialGenerationCannotPublishArtifact(t *testing.T) {
	// 捕获错误变更：Service 在 builder 已写入部分 Agent 落盘内容后仍 rename 发布临时 artifact。
	roots := newTestRoots(t)
	ref := testPluginRef()
	download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
		writeCanonicalPluginPackage(t, destination, ref, "Demo")
		return nil
	})
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, partialAgentLandingBuilder{})
	if _, err := service.Ensure(context.Background(), testCatalogPluginEntry(ref)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
	}
	if _, err := os.Lstat(testArtifactRoot(roots.cache, ref)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published artifact stat error = %v, want absent", err)
	}
}

type partialAgentLandingBuilder struct{}

func (partialAgentLandingBuilder) Build(ref domain.PowerRef, root string) error {
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return err
	}
	pluginPath := filepath.Join(root, "plugins", logicalName)
	canonical, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return err
	}
	blocked, err := pluginartifact.LandingPluginPath(root, domain.AgentHermes, logicalName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(blocked, pluginManifestPath), []byte("block"), 0o600); err != nil {
		return err
	}
	return writeAgentLandings(root, pluginPath, canonical)
}

func (partialAgentLandingBuilder) Validate(domain.PowerRef, string) error {
	return nil
}

func assertGeneratedJSONSchema(t *testing.T, path string, want map[string]any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(contents, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generated JSON %q = %#v, want %#v", path, got, want)
	}
}

// landingFilePath 返回指定 Agent 落盘插件叶子内相对文件的绝对路径。
func landingFilePath(t *testing.T, root string, agent domain.AgentID, logicalName, relative string) string {
	t.Helper()
	landing, err := pluginartifact.LandingPluginPath(root, agent, logicalName)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(landing, filepath.FromSlash(relative))
}

// relativeFiles 返回 root 下全部文件的斜杠分隔相对路径，按字典序排序。
func relativeFiles(t *testing.T, root string) []string {
	t.Helper()
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func pluginartifactTestRef() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
}

func writeCanonicalPluginPackage(t *testing.T, root string, ref domain.PowerRef, description string) {
	t.Helper()
	writeCanonicalPluginPackageManifest(t, root, ref, pluginartifact.APCPluginManifest{
		SchemaVersion: 1, Name: ref.Name, Version: ref.Version, Description: description, Skills: []string{"skills/" + ref.Name},
	})
}

// writeCanonicalPluginPackageV2 写入带本地化展示字段的 v2 canonical 包，
// 用于断言这些字段不会进入派生清单。
func writeCanonicalPluginPackageV2(t *testing.T, root string, ref domain.PowerRef) {
	t.Helper()
	writeCanonicalPluginPackageManifest(t, root, ref, pluginartifact.APCPluginManifest{
		SchemaVersion: 2, Name: ref.Name, NameZH: "演示插件", NameEN: "Demo Plugin", Version: ref.Version,
		Description: "Canonical demo", DescriptionZH: "规范演示插件", Skills: []string{"skills/" + ref.Name},
	})
}

func writeCanonicalPluginPackageManifest(t *testing.T, root string, ref domain.PowerRef, manifest pluginartifact.APCPluginManifest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "skills", ref.Name), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apc-plugin.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if err := os.WriteFile(filepath.Join(root, "skills", ref.Name, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
