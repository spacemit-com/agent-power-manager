package pluginartifact_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

func TestAPCPluginManifestLoadsExactCanonicalSchema(t *testing.T) {
	// 捕获错误变更：canonical loader 未返回精确身份，或返回可被后续调用共享修改的 Skills。
	root, ref := writeCanonicalPlugin(t, `{
  "schema_version": 1,
  "name": "demo",
  "version": "v1.2.3",
  "description": "Canonical demo",
  "skills": ["skills/demo"]
}`)
	got, err := pluginartifact.LoadAPCPluginManifest(ref, root)
	if err != nil {
		t.Fatalf("LoadAPCPluginManifest() error = %v", err)
	}
	want := pluginartifact.APCPluginManifest{
		SchemaVersion: 1,
		Name:          "demo",
		Version:       "v1.2.3",
		Description:   "Canonical demo",
		Skills:        []string{"skills/demo"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadAPCPluginManifest() = %+v, want %+v", got, want)
	}

	got.Skills[0] = "skills/mutated"
	reloaded, err := pluginartifact.LoadAPCPluginManifest(ref, root)
	if err != nil {
		t.Fatalf("second LoadAPCPluginManifest() error = %v", err)
	}
	if !reflect.DeepEqual(reloaded.Skills, []string{"skills/demo"}) {
		t.Fatalf("second Skills = %v, want an independent canonical slice", reloaded.Skills)
	}
}

func TestAPCPluginManifestRejectsInvalidSchemaIdentityAndSkills(t *testing.T) {
	// 捕获错误变更：宽松 JSON、身份归一化或递归 skill 路径绕过 canonical v1 闭集。
	ref := canonicalPluginRef()
	tests := []struct {
		name     string
		manifest string
	}{
		{name: "schema zero", manifest: `{"schema_version":0,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/demo"]}`},
		{name: "schema two", manifest: `{"schema_version":2,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/demo"]}`},
		{name: "wrong name", manifest: `{"schema_version":1,"name":"other","version":"v1.2.3","description":"Demo","skills":["skills/demo"]}`},
		{name: "wrong version", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.4","description":"Demo","skills":["skills/demo"]}`},
		{name: "native version", manifest: `{"schema_version":1,"name":"demo","version":"1.2.3","description":"Demo","skills":["skills/demo"]}`},
		{name: "blank description", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":" \t","skills":["skills/demo"]}`},
		{name: "missing skills", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo"}`},
		{name: "empty skills", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":[]}`},
		{name: "duplicate skill", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/demo","skills/demo"]}`},
		{name: "bare skill", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["demo"]}`},
		{name: "skills root", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills"]}`},
		{name: "absolute skill", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["/skills/demo"]}`},
		{name: "parent skill", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/../demo"]}`},
		{name: "nested skill", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/group/demo"]}`},
		{name: "backslash skill", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills\\demo"]}`},
		{name: "unknown field", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/demo"],"commands":[]}`},
		{name: "duplicate field", manifest: `{"schema_version":1,"name":"demo","name":"other","version":"v1.2.3","description":"Demo","skills":["skills/demo"]}`},
		{name: "trailing value", manifest: `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":["skills/demo"]} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, _ := writeCanonicalPlugin(t, test.manifest)
			if _, err := pluginartifact.LoadAPCPluginManifest(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LoadAPCPluginManifest() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestAPCPluginManifestRejectsOpenOrNonPhysicalSkillWorld(t *testing.T) {
	// 捕获错误变更：只相信声明而不复验 immediate skills 目录、必需文件或唯一物理 inode。
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "missing declared directory",
			mutate: func(t *testing.T, root string) {
				if err := os.RemoveAll(filepath.Join(root, "skills", "demo")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra immediate directory",
			mutate: func(t *testing.T, root string) {
				writeCanonicalSkill(t, filepath.Join(root, "skills", "extra"))
			},
		},
		{
			name: "file in skills root",
			mutate: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "skills", "extra.txt"), []byte("extra"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing SKILL.md",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "skills", "demo", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing skill-card.md",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "skills", "demo", "skill-card.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink marker",
			mutate: func(t *testing.T, root string) {
				marker := filepath.Join(root, "skills", "demo", "skill-card.md")
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "skills", "demo", "SKILL.md"), marker); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hardlinked manifest",
			mutate: func(t *testing.T, root string) {
				if err := os.Link(filepath.Join(root, "apc-plugin.json"), filepath.Join(t.TempDir(), "manifest-link.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hardlinked marker",
			mutate: func(t *testing.T, root string) {
				if err := os.Link(filepath.Join(root, "skills", "demo", "SKILL.md"), filepath.Join(t.TempDir(), "skill-link.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeCanonicalPlugin(t, validCanonicalManifest())
			test.mutate(t, root)
			if _, err := pluginartifact.LoadAPCPluginManifest(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LoadAPCPluginManifest() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestAPCPluginRawPackageRejectsGeneratedAgentPaths(t *testing.T) {
	// 捕获错误变更：Cloud raw package 夹带任一 Agent 原生配置或派生 marketplace。
	tests := []struct {
		name     string
		relative string
		dirOnly  bool
	}{
		{name: "Codex directory", relative: ".codex-plugin", dirOnly: true},
		{name: "Claude manifest", relative: ".claude-plugin/plugin.json"},
		{name: "Hermes manifest", relative: ".hermes-plugin/plugin.yaml"},
		{name: "Codex marketplace", relative: ".agents/plugins/marketplace.json"},
		{name: "nested native path", relative: "skills/demo/.codex-plugin/plugin.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeCanonicalPlugin(t, validCanonicalManifest())
			path := filepath.Join(root, filepath.FromSlash(test.relative))
			if test.dirOnly {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("generated"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := pluginartifact.ValidateRawPackage(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateRawPackage() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func canonicalPluginRef() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}
}

func validCanonicalManifest() string {
	return `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Canonical demo","skills":["skills/demo"]}`
}

func writeCanonicalPlugin(t *testing.T, manifest string) (string, domain.PowerRef) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "apc-plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	writeCanonicalSkill(t, filepath.Join(root, "skills", "demo"))
	return root, canonicalPluginRef()
}

func writeCanonicalSkill(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
