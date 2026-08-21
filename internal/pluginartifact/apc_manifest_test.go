package pluginartifact_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestAPCPluginManifestRejectsNonCanonicalPluginAndSkillNames(t *testing.T) {
	// 捕获错误变更：APC Plugin 或声明 Skill 接受大小写、标点、双连字符及超长名称。
	invalidNames := []string{"Demo", "a--b", "a:b", "-demo", "demo-", strings.Repeat("a", 65)}
	for _, invalidName := range invalidNames {
		t.Run("plugin/"+invalidName, func(t *testing.T) {
			root := t.TempDir()
			writeCanonicalSkill(t, filepath.Join(root, "skills", "valid-skill"))
			manifest := fmt.Sprintf(`{"schema_version":1,"name":%q,"version":"v1.2.3","description":"Demo","skills":["skills/valid-skill"]}`, invalidName)
			if err := os.WriteFile(filepath.Join(root, "apc-plugin.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			ref := domain.PowerRef{Name: invalidName, Type: domain.PowerTypePlugin, Version: "v1.2.3"}
			if _, err := pluginartifact.LoadAPCPluginManifest(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LoadAPCPluginManifest() error = %v, want ErrInvalid", err)
			}
		})

		t.Run("skill/"+invalidName, func(t *testing.T) {
			root := t.TempDir()
			writeCanonicalSkill(t, filepath.Join(root, "skills", invalidName))
			manifest := fmt.Sprintf(`{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Demo","skills":[%q]}`, "skills/"+invalidName)
			if err := os.WriteFile(filepath.Join(root, "apc-plugin.json"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := pluginartifact.LoadAPCPluginManifest(canonicalPluginRef(), root); !errors.Is(err, pluginartifact.ErrInvalid) {
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

func TestAPCPluginRawPackageRejectsPathsOutsideDeclaredSkillWorld(t *testing.T) {
	// 捕获错误变更：Cloud raw package 在根目录夹带未声明能力、任意 payload、原生配置或 marketplace alias。
	tests := []struct {
		name     string
		relative string
		dirOnly  bool
	}{
		{name: "arbitrary root file", relative: "payload.txt"},
		{name: "commands capability", relative: "commands/demo.md"},
		{name: "hooks capability", relative: "hooks/hooks.json"},
		{name: "agents capability", relative: "agents/demo.yaml"},
		{name: "MCP capability", relative: "MCP/server.json"},
		{name: "agents metadata directory", relative: ".agents/config.json"},
		{name: "Codex directory", relative: ".codex-plugin", dirOnly: true},
		{name: "Claude manifest", relative: ".claude-plugin/plugin.json"},
		{name: "Hermes manifest", relative: ".hermes-plugin/plugin.yaml"},
		{name: "Codex marketplace", relative: ".agents/plugins/marketplace.json"},
		{name: "Claude marketplace alias", relative: "marketplaces/.claude-plugin/marketplace.json"},
		{name: "wrong-case canonical marker alias", relative: "APC-PLUGIN.JSON"},
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

func TestAPCPluginRawPackageAllowsSupportingFilesBelowDeclaredSkills(t *testing.T) {
	// 捕获错误变更：路径闭集错误限制声明 Skill 内部的合法支持文件或目录名称。
	root, ref := writeCanonicalPlugin(t, validCanonicalManifest())
	for relative, contents := range map[string]string{
		"skills/demo/references/guide.md":                "guide\n",
		"skills/demo/scripts/check.sh":                   "#!/bin/sh\n",
		"skills/demo/.codex-plugin/plugin.json":          "supporting data\n",
		"skills/demo/commands/hooks/agents/MCP/data.txt": "supporting data\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pluginartifact.ValidateRawPackage(ref, root); err != nil {
		t.Fatalf("ValidateRawPackage() error = %v", err)
	}
}

func TestAPCPluginManifestRejectsWrongCaseCanonicalEntries(t *testing.T) {
	// 捕获错误变更：大小写不敏感文件系统把错误拼写的 canonical marker 当作精确路径。
	tests := []struct {
		name     string
		relative string
		wrong    string
	}{
		{name: "manifest", relative: "apc-plugin.json", wrong: "APC-PLUGIN.JSON"},
		{name: "skills root", relative: "skills", wrong: "Skills"},
		{name: "skill directory", relative: "skills/demo", wrong: "skills/Demo"},
		{name: "SKILL marker", relative: "skills/demo/SKILL.md", wrong: "skills/demo/Skill.md"},
		{name: "skill-card marker", relative: "skills/demo/skill-card.md", wrong: "skills/demo/SKILL-CARD.MD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeCanonicalPlugin(t, validCanonicalManifest())
			renameEntryCase(t, root, test.relative, test.wrong)
			if _, err := pluginartifact.LoadAPCPluginManifest(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LoadAPCPluginManifest() error = %v, want ErrInvalid", err)
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

func renameEntryCase(t *testing.T, root, relative, wrongRelative string) {
	t.Helper()
	source := filepath.Join(root, filepath.FromSlash(relative))
	target := filepath.Join(root, filepath.FromSlash(wrongRelative))
	if err := os.Rename(source, target); err != nil {
		t.Skipf("filesystem cannot rename %q to wrong-case spelling %q: %v", relative, wrongRelative, err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == filepath.Base(target) {
			return
		}
	}
	t.Skipf("filesystem did not preserve wrong-case spelling %q", wrongRelative)
}
