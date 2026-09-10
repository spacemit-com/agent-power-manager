package pluginresource

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"go.yaml.in/yaml/v3"
)

func TestAgentManifestGenerationIsDeterministicAndUsesExactSchemas(t *testing.T) {
	// 捕获错误变更：同一 canonical index 生成不同字节、错误版本格式或 Agent schema 字段漂移。
	manifest := pluginartifact.APCPluginManifest{
		SchemaVersion: 1,
		Name:          "demo",
		Version:       "v1.2.3",
		Description:   "Quoted \"description\"\nvalue: true",
		Skills:        []string{"skills/demo"},
	}
	first := filepath.Join(t.TempDir(), "plugin")
	second := filepath.Join(t.TempDir(), "plugin")
	for _, root := range []string{first, second} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeAgentManifests(root, manifest); err != nil {
			t.Fatalf("writeAgentManifests() error = %v", err)
		}
	}

	paths := []string{
		codexManifestPath,
		claudeManifestPath,
		hermesManifestPath,
		hermesInitPath,
	}
	for _, relative := range paths {
		firstBytes, err := os.ReadFile(filepath.Join(first, relative))
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := os.ReadFile(filepath.Join(second, relative))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(firstBytes, secondBytes) {
			t.Fatalf("generated %q differs across roots\nfirst: %q\nsecond: %q", relative, firstBytes, secondBytes)
		}
		info, err := os.Stat(filepath.Join(first, relative))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("generated %q mode = %o, want 0600", relative, info.Mode().Perm())
		}
	}

	assertGeneratedJSONSchema(t, filepath.Join(first, codexManifestPath), map[string]any{
		"name":        "demo",
		"version":     "1.2.3",
		"description": "Quoted \"description\"\nvalue: true",
		"skills":      "./skills/",
	})
	assertGeneratedJSONSchema(t, filepath.Join(first, claudeManifestPath), map[string]any{
		"name":        "demo",
		"version":     "1.2.3",
		"description": "Quoted \"description\"\nvalue: true",
		"skills":      []any{"./skills/"},
	})

	hermesBytes, err := os.ReadFile(filepath.Join(first, hermesManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	var hermes map[string]any
	if err := yaml.Unmarshal(hermesBytes, &hermes); err != nil {
		t.Fatal(err)
	}
	wantHermes := map[string]any{
		"name":        "demo",
		"version":     "1.2.3",
		"description": "Quoted \"description\"\nvalue: true",
		"author":      "APM",
	}
	if !reflect.DeepEqual(hermes, wantHermes) {
		t.Fatalf("Hermes manifest = %#v, want %#v", hermes, wantHermes)
	}
	initBytes, err := os.ReadFile(filepath.Join(first, hermesInitPath))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(initBytes), "def register(ctx):\n    return None\n"; got != want {
		t.Fatalf("Hermes __init__.py = %q, want %q", got, want)
	}
}

func TestAgentManifestGenerationDoesNotOverwriteExistingDerivedFile(t *testing.T) {
	// 捕获错误变更：生成器覆盖已存在节点，使 raw 校验与不可变临时树边界失去意义。
	root := t.TempDir()
	target := filepath.Join(root, codexManifestPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := pluginartifact.APCPluginManifest{
		SchemaVersion: 1, Name: "demo", Version: "v1.2.3", Description: "Demo", Skills: []string{"skills/demo"},
	}
	if err := writeAgentManifests(root, manifest); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("writeAgentManifests() error = %v, want ErrInvalidArtifact", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "existing" {
		t.Fatalf("existing target = %q, want unchanged", contents)
	}
}

func TestAgentManifestPartialGenerationCannotValidateSharedArtifact(t *testing.T) {
	// 捕获错误变更：前几个文件落地后，后续写失败仍被共享 validator 当作可发布完整 artifact。
	root := t.TempDir()
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}
	pluginPath := filepath.Join(root, "plugins", ref.Name)
	writeCanonicalPluginPackage(t, pluginPath, ref, "Demo")
	if err := os.WriteFile(filepath.Join(pluginPath, hermesManifestPath), []byte("block directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAgentManifests(pluginPath, manifest); err == nil {
		t.Fatal("writeAgentManifests() error = nil, want blocked Hermes generation")
	}
	if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
		t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
	}
}

func TestAgentManifestPartialGenerationCannotPublishArtifact(t *testing.T) {
	// 捕获错误变更：Service 在 builder 已写入部分 Agent 文件后仍 rename 发布临时 artifact。
	roots := newTestRoots(t)
	ref := testPluginRef()
	download := pluginDownloadFunc(func(_ context.Context, _ domain.PowerRef, destination string) error {
		writeCanonicalPluginPackage(t, destination, ref, "Demo")
		return nil
	})
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, download, partialAgentManifestBuilder{})
	if _, err := service.Ensure(context.Background(), testCatalogPluginEntry(ref)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
	}
	if _, err := os.Lstat(testArtifactRoot(roots.cache, ref)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published artifact stat error = %v, want absent", err)
	}
}

type partialAgentManifestBuilder struct{}

func (partialAgentManifestBuilder) Build(ref domain.PowerRef, _ string, pluginPath string) error {
	canonical, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pluginPath, hermesManifestPath), []byte("block directory"), 0o600); err != nil {
		return err
	}
	return writeAgentManifests(pluginPath, canonical)
}

func (partialAgentManifestBuilder) Validate(domain.PowerRef, string) error {
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

func writeCanonicalPluginPackage(t *testing.T, root string, ref domain.PowerRef, description string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "skills", ref.Name), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := pluginartifact.APCPluginManifest{
		SchemaVersion: 1, Name: ref.Name, Version: ref.Version, Description: description, Skills: []string{"skills/" + ref.Name},
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
