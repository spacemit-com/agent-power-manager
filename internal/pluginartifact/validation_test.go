package pluginartifact_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
)

func TestValidateSharedAcceptsCanonicalArtifactAndAllDerivedFiles(t *testing.T) {
	root, ref := writeValidSharedArtifact(t)
	if err := pluginartifact.ValidateShared(ref, root); err != nil {
		t.Fatalf("ValidateShared() error = %v", err)
	}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		if err := pluginartifact.ValidateMarketplace(agent, ref, root); err != nil {
			t.Fatalf("ValidateMarketplace(%q) error = %v", agent, err)
		}
	}
}

func TestValidateSharedRequiresEveryDerivedFile(t *testing.T) {
	// 捕获错误变更：部分 Agent 生成或任一缺失 marketplace 被当作完整共享 artifact。
	for _, relative := range []string{
		"plugins/demo/.codex-plugin/plugin.json",
		"plugins/demo/.claude-plugin/plugin.json",
		"plugins/demo/plugin.yaml",
		"plugins/demo/__init__.py",
		".agents/plugins/marketplace.json",
		".claude-plugin/marketplace.json",
	} {
		t.Run(relative, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
				t.Fatal(err)
			}
			if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateSharedRejectsPathsOutsideFinalClosedWorld(t *testing.T) {
	// 捕获错误变更：最终共享 artifact 接受 canonical Skill 闭集与六个派生文件之外的额外能力或 alias。
	tests := []struct {
		name     string
		relative string
		dirOnly  bool
	}{
		{name: "root payload", relative: "payload.txt"},
		{name: "extra logical Plugin", relative: "plugins/other/apc-plugin.json"},
		{name: "commands capability", relative: "plugins/demo/commands/demo.md"},
		{name: "hooks capability", relative: "plugins/demo/hooks/hooks.json"},
		{name: "agents capability", relative: "plugins/demo/agents/demo.yaml"},
		{name: "MCP capability", relative: "plugins/demo/MCP/server.json"},
		{name: "Codex native extra file", relative: "plugins/demo/.codex-plugin/extra.json"},
		{name: "Claude native extra directory", relative: "plugins/demo/.claude-plugin/extra/value.json"},
		{name: "Hermes legacy manifest path", relative: "plugins/demo/.hermes-plugin/plugin.yaml"},
		{name: "Codex marketplace sibling", relative: ".agents/plugins/extra.json"},
		{name: "Codex marketplace parent extra", relative: ".agents/extra.json"},
		{name: "Claude marketplace sibling", relative: ".claude-plugin/extra.json"},
		{name: "marketplace alias", relative: "marketplaces/.agents/plugins/marketplace.json"},
		{name: "wrong-case Codex native alias", relative: "plugins/demo/.CODEX-PLUGIN/plugin.json"},
		{name: "wrong-case marketplace alias", relative: ".AGENTS/plugins/marketplace.json"},
		{name: "empty unexpected directory", relative: "plugins/demo/commands", dirOnly: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			path := filepath.Join(root, filepath.FromSlash(test.relative))
			if test.dirOnly {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("extra\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateMarketplaceRejectsExtraNativeAndMarketplaceFiles(t *testing.T) {
	// 捕获错误变更：Agent-specific recovery 校验绕过完整共享 artifact 的相对路径闭集。
	for _, relative := range []string{
		"plugins/demo/commands/demo.md",
		"plugins/demo/extra.py",
		".agents/plugins/debug.json",
		".claude-plugin/debug.json",
	} {
		t.Run(relative, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			path := filepath.Join(root, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("extra\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
				if err := pluginartifact.ValidateMarketplace(agent, ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
					t.Fatalf("ValidateMarketplace(%q) error = %v, want ErrInvalid", agent, err)
				}
			}
		})
	}
}

func TestValidateSharedAllowsSupportingFilesBelowDeclaredSkills(t *testing.T) {
	root, ref := writeValidSharedArtifact(t)
	for relative, contents := range map[string]string{
		"plugins/demo/skills/demo/references/guide.md":         "guide\n",
		"plugins/demo/skills/demo/.agents/plugins/config.json": "supporting data\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pluginartifact.ValidateShared(ref, root); err != nil {
		t.Fatalf("ValidateShared() error = %v", err)
	}
}

func TestValidateSharedRejectsWrongCaseGeneratedEntries(t *testing.T) {
	// 捕获错误变更：大小写不敏感文件系统把错误拼写的原生或 marketplace 路径当作 canonical 文件。
	tests := []struct {
		name     string
		relative string
		wrong    string
	}{
		{name: "plugins root", relative: "plugins", wrong: "Plugins"},
		{name: "Plugin directory", relative: "plugins/demo", wrong: "plugins/Demo"},
		{name: "Codex directory", relative: "plugins/demo/.codex-plugin", wrong: "plugins/demo/.CODEX-PLUGIN"},
		{name: "Codex manifest", relative: "plugins/demo/.codex-plugin/plugin.json", wrong: "plugins/demo/.codex-plugin/PLUGIN.JSON"},
		{name: "Claude directory", relative: "plugins/demo/.claude-plugin", wrong: "plugins/demo/.CLAUDE-PLUGIN"},
		{name: "Claude manifest", relative: "plugins/demo/.claude-plugin/plugin.json", wrong: "plugins/demo/.claude-plugin/PLUGIN.JSON"},
		{name: "Hermes manifest", relative: "plugins/demo/plugin.yaml", wrong: "plugins/demo/PLUGIN.YAML"},
		{name: "Hermes initializer", relative: "plugins/demo/__init__.py", wrong: "plugins/demo/__INIT__.PY"},
		{name: "agents directory", relative: ".agents", wrong: ".AGENTS"},
		{name: "marketplace plugins directory", relative: ".agents/plugins", wrong: ".agents/Plugins"},
		{name: "Codex marketplace", relative: ".agents/plugins/marketplace.json", wrong: ".agents/plugins/Marketplace.json"},
		{name: "Claude marketplace directory", relative: ".claude-plugin", wrong: ".CLAUDE-PLUGIN"},
		{name: "Claude marketplace", relative: ".claude-plugin/marketplace.json", wrong: ".claude-plugin/Marketplace.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			renameEntryCase(t, root, test.relative, test.wrong)
			if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateSharedRejectsGeneratedSchemaAndCanonicalDrift(t *testing.T) {
	// 捕获错误变更：validator 使用比 builder 更宽松的字段类型/schema，或未与 canonical index 比较。
	tests := []struct {
		name        string
		relative    string
		old         string
		replacement string
	}{
		{name: "canonical description drift", relative: "plugins/demo/apc-plugin.json", old: `"Canonical description"`, replacement: `"Other description"`},
		{name: "Codex description drift", relative: "plugins/demo/.codex-plugin/plugin.json", old: `"Canonical description"`, replacement: `"Other description"`},
		{name: "Codex missing description", relative: "plugins/demo/.codex-plugin/plugin.json", old: `,"description":"Canonical description"`, replacement: ``},
		{name: "Codex skills type", relative: "plugins/demo/.codex-plugin/plugin.json", old: `"skills":"./skills/"`, replacement: `"skills":["./skills/"]`},
		{name: "Codex unknown field", relative: "plugins/demo/.codex-plugin/plugin.json", old: `"skills":"./skills/"`, replacement: `"skills":"./skills/","author":"APM"`},
		{name: "Claude skills type", relative: "plugins/demo/.claude-plugin/plugin.json", old: `"skills":["./skills/"]`, replacement: `"skills":"./skills/"`},
		{name: "Claude missing description", relative: "plugins/demo/.claude-plugin/plugin.json", old: `,"description":"Canonical description"`, replacement: ``},
		{name: "Claude duplicate field", relative: "plugins/demo/.claude-plugin/plugin.json", old: `"name":"demo"`, replacement: `"name":"demo","name":"other"`},
		{name: "Hermes author drift", relative: "plugins/demo/plugin.yaml", old: "author: APM", replacement: "author: Other"},
		{name: "Hermes missing author", relative: "plugins/demo/plugin.yaml", old: "author: APM\n", replacement: ""},
		{name: "Hermes unknown field", relative: "plugins/demo/plugin.yaml", old: "author: APM", replacement: "author: APM\nskills: ./skills/"},
		{name: "Hermes initializer drift", relative: "plugins/demo/__init__.py", old: "return None", replacement: "pass"},
		{name: "Codex marketplace drift", relative: ".agents/plugins/marketplace.json", old: `"path":"./plugins/demo"`, replacement: `"path":"../demo"`},
		{name: "Claude marketplace drift", relative: ".claude-plugin/marketplace.json", old: `"owner":{"name":"APM"}`, replacement: `"owner":{"name":"Other"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			replaceArtifactText(t, filepath.Join(root, filepath.FromSlash(test.relative)), test.old, test.replacement)
			if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateMarketplaceRequiresCompleteSharedArtifact(t *testing.T) {
	// 捕获错误变更：recovery 只校验目标 Agent，忽略共享 artifact 中另一份派生配置损坏。
	root, ref := writeValidSharedArtifact(t)
	if err := os.Remove(filepath.Join(root, "plugins", ref.Name, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if err := pluginartifact.ValidateMarketplace(domain.AgentCodex, ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
		t.Fatalf("ValidateMarketplace() error = %v, want ErrInvalid", err)
	}
}

func TestValidateMarketplaceRejectsUnsupportedAgent(t *testing.T) {
	root, ref := writeValidSharedArtifact(t)
	for _, agent := range []domain.AgentID{domain.AgentHermes, domain.AgentBibit, domain.AgentID("unknown")} {
		t.Run(string(agent), func(t *testing.T) {
			if err := pluginartifact.ValidateMarketplace(agent, ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateMarketplace(%q) error = %v, want ErrInvalid", agent, err)
			}
		})
	}
}

func TestValidateSharedRejectsUnsafePhysicalLayout(t *testing.T) {
	t.Run("logical root escape", func(t *testing.T) {
		root, ref := writeValidSharedArtifact(t)
		ref.Name = "../escape"
		if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})

	t.Run("root symlink", func(t *testing.T) {
		root, ref := writeValidSharedArtifact(t)
		link := filepath.Join(t.TempDir(), "artifact-link")
		if err := os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}
		if err := pluginartifact.ValidateShared(ref, link); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})

	t.Run("nested symlink", func(t *testing.T) {
		root, ref := writeValidSharedArtifact(t)
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "plugins", ref.Name, "escape")); err != nil {
			t.Fatal(err)
		}
		if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})

	t.Run("hardlink", func(t *testing.T) {
		root, ref := writeValidSharedArtifact(t)
		if err := os.Link(filepath.Join(root, ".agents", "plugins", "marketplace.json"), filepath.Join(t.TempDir(), "marketplace.json")); err != nil {
			t.Fatal(err)
		}
		if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})

	t.Run("FIFO", func(t *testing.T) {
		root, ref := writeValidSharedArtifact(t)
		createFIFO(t, filepath.Join(root, "plugins", ref.Name, "pipe"))
		if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})
}

func writeValidSharedArtifact(t *testing.T) (string, domain.PowerRef) {
	t.Helper()
	root := t.TempDir()
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.2.3"}
	files := map[string]string{
		"plugins/demo/apc-plugin.json":            `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Canonical description","skills":["skills/demo"]}`,
		"plugins/demo/skills/demo/SKILL.md":       "# Demo\n",
		"plugins/demo/skills/demo/skill-card.md":  "# Skill Card\n",
		"plugins/demo/.codex-plugin/plugin.json":  `{"name":"demo","version":"1.2.3","description":"Canonical description","skills":"./skills/"}`,
		"plugins/demo/.claude-plugin/plugin.json": `{"name":"demo","version":"1.2.3","description":"Canonical description","skills":["./skills/"]}`,
		"plugins/demo/plugin.yaml": "name: demo\nversion: 1.2.3\ndescription: Canonical description\nauthor: APM\n",
		"plugins/demo/__init__.py": "def register(ctx):\n    return None\n",
		".agents/plugins/marketplace.json":        `{"name":"apm-demo","interface":{"displayName":"demo"},"plugins":[{"name":"demo","source":{"source":"local","path":"./plugins/demo"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]}`,
		".claude-plugin/marketplace.json":         `{"name":"apm-demo","owner":{"name":"APM"},"metadata":{"description":"Canonical description","version":"1.2.3"},"plugins":[{"name":"demo","source":"./plugins/demo","description":"Canonical description"}]}`,
	}
	for relative, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, ref
}

func replaceArtifactText(t *testing.T, path, old, replacement string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(contents), old, replacement, 1)
	if updated == string(contents) {
		t.Fatalf("fixture %q does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}
