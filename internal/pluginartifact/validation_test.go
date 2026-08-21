package pluginartifact_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
		"plugins/demo/.hermes-plugin/plugin.yaml",
		"plugins/demo/.hermes-plugin/__init__.py",
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
		{name: "Hermes author drift", relative: "plugins/demo/.hermes-plugin/plugin.yaml", old: "author: APM", replacement: "author: Other"},
		{name: "Hermes missing author", relative: "plugins/demo/.hermes-plugin/plugin.yaml", old: "author: APM\n", replacement: ""},
		{name: "Hermes unknown field", relative: "plugins/demo/.hermes-plugin/plugin.yaml", old: "author: APM", replacement: "author: APM\nskills: ./skills/"},
		{name: "Hermes initializer drift", relative: "plugins/demo/.hermes-plugin/__init__.py", old: "return None", replacement: "pass"},
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
		if err := syscall.Mkfifo(filepath.Join(root, "plugins", ref.Name, "pipe"), 0o600); err != nil {
			t.Fatal(err)
		}
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
		"plugins/demo/.hermes-plugin/plugin.yaml": "name: demo\nversion: 1.2.3\ndescription: Canonical description\nauthor: APM\n",
		"plugins/demo/.hermes-plugin/__init__.py": "def register(ctx):\n    return None\n",
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
