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
	// 捕获错误变更：部分 Agent 落盘或任一缺失 marketplace 被当作完整共享 artifact。
	for _, relative := range []string{
		"landing/codex/plugins/demo/.codex-plugin/plugin.json",
		"landing/claude-code/plugins/demo/.claude-plugin/plugin.json",
		"landing/hermes-agent/plugins/demo/plugin.yaml",
		"landing/hermes-agent/plugins/demo/__init__.py",
		"landing/bibit/plugins/demo/plugin.yaml",
		"landing/bibit/plugins/demo/skills/demo/SKILL.md",
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
	// 捕获错误变更：最终 artifact 接受 canonical 闭集、四个落盘叶子与两个 marketplace 之外的额外条目。
	tests := []struct {
		name     string
		relative string
		dirOnly  bool
	}{
		{name: "root payload", relative: "payload.txt"},
		{name: "extra logical Plugin", relative: "plugins/other/apc-plugin.json"},
		{name: "canonical commands capability", relative: "plugins/demo/commands/demo.md"},
		{name: "canonical hooks capability", relative: "plugins/demo/hooks/hooks.json"},
		{name: "canonical agents capability", relative: "plugins/demo/agents/demo.yaml"},
		{name: "canonical MCP capability", relative: "plugins/demo/MCP/server.json"},
		{name: "canonical Codex native extra file", relative: "plugins/demo/.codex-plugin/extra.json"},
		{name: "canonical Claude native extra directory", relative: "plugins/demo/.claude-plugin/extra/value.json"},
		{name: "canonical filesystem manifest", relative: "plugins/demo/plugin.yaml"},
		{name: "canonical ambiguous manifest", relative: "plugins/demo/plugin.yml"},
		{name: "canonical legacy manifest path", relative: "plugins/demo/.hermes-plugin/plugin.yaml"},
		{name: "landing unknown Agent", relative: "landing/unknown/plugins/demo/plugin.yaml"},
		{name: "landing Codex commands capability", relative: "landing/codex/plugins/demo/commands/demo.md"},
		{name: "landing Codex Claude manifest", relative: "landing/codex/plugins/demo/.claude-plugin/plugin.json"},
		{name: "landing Codex filesystem manifest", relative: "landing/codex/plugins/demo/plugin.yaml"},
		{name: "landing Claude Codex manifest", relative: "landing/claude-code/plugins/demo/.codex-plugin/plugin.json"},
		{name: "landing Claude initializer", relative: "landing/claude-code/plugins/demo/__init__.py"},
		{name: "landing Hermes canonical manifest", relative: "landing/hermes-agent/plugins/demo/apc-plugin.json"},
		{name: "landing Hermes Codex manifest", relative: "landing/hermes-agent/plugins/demo/.codex-plugin/plugin.json"},
		{name: "landing Hermes extra file", relative: "landing/hermes-agent/plugins/demo/extra.py"},
		{name: "landing Bibit initializer", relative: "landing/bibit/plugins/demo/__init__.py"},
		{name: "landing Bibit canonical manifest", relative: "landing/bibit/plugins/demo/apc-plugin.json"},
		{name: "landing Bibit Claude manifest", relative: "landing/bibit/plugins/demo/.claude-plugin/plugin.json"},
		{name: "landing Bibit extra file", relative: "landing/bibit/plugins/demo/extra.py"},
		{name: "landing extra plugin under Agent", relative: "landing/bibit/plugins/other/plugin.yaml"},
		{name: "Codex marketplace sibling", relative: ".agents/plugins/extra.json"},
		{name: "Codex marketplace parent extra", relative: ".agents/extra.json"},
		{name: "Claude marketplace sibling", relative: ".claude-plugin/extra.json"},
		{name: "marketplace alias", relative: "marketplaces/.agents/plugins/marketplace.json"},
		{name: "wrong-case Codex native alias", relative: "landing/codex/plugins/demo/.CODEX-PLUGIN/plugin.json"},
		{name: "wrong-case marketplace alias", relative: ".AGENTS/plugins/marketplace.json"},
		{name: "empty unexpected directory", relative: "plugins/demo/commands", dirOnly: true},
		{name: "empty unexpected landing directory", relative: "landing/codex/plugins/demo/hooks", dirOnly: true},
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
		"landing/codex/plugins/demo/extra.py",
		"landing/claude-code/plugins/demo/extra.py",
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
	plugins := []string{"plugins/demo"}
	for _, agent := range pluginartifact.PluginLandingAgents() {
		plugins = append(plugins, "landing/"+string(agent)+"/plugins/demo")
	}
	for _, plugin := range plugins {
		for relative, contents := range map[string]string{
			"skills/demo/references/guide.md":       "guide\n",
			"skills/demo/.agents/plugins/conf.json": "supporting data\n",
		} {
			path := filepath.Join(root, filepath.FromSlash(plugin+"/"+relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := pluginartifact.ValidateShared(ref, root); err != nil {
		t.Fatalf("ValidateShared() error = %v", err)
	}
}

func TestValidateSharedRejectsSkillPayloadDriftFromCanonical(t *testing.T) {
	// 捕获错误变更：落盘 Skill 只有路径完整，内容却被替换或删减，安装目录得到与 canonical 不一致的 Skill。
	for _, relative := range []string{
		"landing/codex/plugins/demo/skills/demo/SKILL.md",
		"landing/hermes-agent/plugins/demo/skills/demo/SKILL.md",
		"landing/bibit/plugins/demo/skills/demo/SKILL.md",
	} {
		t.Run("modified "+relative, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			replaceArtifactText(t, filepath.Join(root, filepath.FromSlash(relative)), "# Demo\n", "# Other\n")
			if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
			}
		})
	}

	t.Run("removed landing skill file", func(t *testing.T) {
		root, ref := writeValidSharedArtifact(t)
		if err := os.Remove(filepath.Join(root, filepath.FromSlash("landing/bibit/plugins/demo/skills/demo/skill-card.md"))); err != nil {
			t.Fatal(err)
		}
		if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})
}

func TestValidateSharedRejectsWrongCaseGeneratedEntries(t *testing.T) {
	// 捕获错误变更：大小写不敏感文件系统把错误拼写的原生、落盘或 marketplace 路径当作 canonical 文件。
	tests := []struct {
		name     string
		relative string
		wrong    string
	}{
		{name: "plugins root", relative: "plugins", wrong: "Plugins"},
		{name: "Plugin directory", relative: "plugins/demo", wrong: "plugins/Demo"},
		{name: "canonical Codex directory", relative: "plugins/demo/.codex-plugin", wrong: "plugins/demo/.CODEX-PLUGIN"},
		{name: "landing root", relative: "landing", wrong: "Landing"},
		{name: "landing Agent directory", relative: "landing/codex", wrong: "landing/Codex"},
		{name: "landing Codex directory", relative: "landing/codex/plugins/demo/.codex-plugin", wrong: "landing/codex/plugins/demo/.CODEX-PLUGIN"},
		{name: "landing Codex manifest", relative: "landing/codex/plugins/demo/.codex-plugin/plugin.json", wrong: "landing/codex/plugins/demo/.codex-plugin/PLUGIN.JSON"},
		{name: "landing Claude directory", relative: "landing/claude-code/plugins/demo/.claude-plugin", wrong: "landing/claude-code/plugins/demo/.CLAUDE-PLUGIN"},
		{name: "landing filesystem manifest", relative: "landing/bibit/plugins/demo/plugin.yaml", wrong: "landing/bibit/plugins/demo/PLUGIN.YAML"},
		{name: "landing Hermes initializer", relative: "landing/hermes-agent/plugins/demo/__init__.py", wrong: "landing/hermes-agent/plugins/demo/__INIT__.PY"},
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
		{name: "canonical skills drift", relative: "plugins/demo/apc-plugin.json", old: `["skills/demo"]`, replacement: `["skills/other"]`},
		{name: "Codex description drift", relative: "landing/codex/plugins/demo/.codex-plugin/plugin.json", old: `"Canonical description"`, replacement: `"Other description"`},
		{name: "Codex missing description", relative: "landing/codex/plugins/demo/.codex-plugin/plugin.json", old: `,"description":"Canonical description"`, replacement: ``},
		{name: "Codex skills type", relative: "landing/codex/plugins/demo/.codex-plugin/plugin.json", old: `"skills":"./skills/"`, replacement: `"skills":["./skills/"]`},
		{name: "Codex unknown field", relative: "landing/codex/plugins/demo/.codex-plugin/plugin.json", old: `"skills":"./skills/"`, replacement: `"skills":"./skills/","author":"APM"`},
		{name: "Claude skills type", relative: "landing/claude-code/plugins/demo/.claude-plugin/plugin.json", old: `"skills":["./skills/"]`, replacement: `"skills":"./skills/"`},
		{name: "Claude missing description", relative: "landing/claude-code/plugins/demo/.claude-plugin/plugin.json", old: `,"description":"Canonical description"`, replacement: ``},
		{name: "Claude duplicate field", relative: "landing/claude-code/plugins/demo/.claude-plugin/plugin.json", old: `"name":"demo"`, replacement: `"name":"demo","name":"other"`},
		{name: "Claude unknown field", relative: "landing/claude-code/plugins/demo/.claude-plugin/plugin.json", old: `"skills":["./skills/"]`, replacement: `"skills":["./skills/"],"author":"APM"`},
		{name: "filesystem schema version missing", relative: "landing/bibit/plugins/demo/plugin.yaml", old: "schema_version: 1\n", replacement: ""},
		{name: "filesystem schema version drift", relative: "landing/bibit/plugins/demo/plugin.yaml", old: "schema_version: 1", replacement: "schema_version: 2"},
		{name: "filesystem schema version type", relative: "landing/bibit/plugins/demo/plugin.yaml", old: "schema_version: 1", replacement: `schema_version: "1"`},
		{name: "filesystem author added", relative: "landing/bibit/plugins/demo/plugin.yaml", old: "description: Canonical description\n", replacement: "description: Canonical description\nauthor: APM\n"},
		{name: "filesystem unknown field", relative: "landing/bibit/plugins/demo/plugin.yaml", old: "description: Canonical description\n", replacement: "description: Canonical description\nskills: ./skills/\n"},
		{name: "filesystem value drift", relative: "landing/bibit/plugins/demo/plugin.yaml", old: "name: demo", replacement: "name: other"},
		{name: "Hermes author added", relative: "landing/hermes-agent/plugins/demo/plugin.yaml", old: "description: Canonical description\n", replacement: "description: Canonical description\nauthor: APM\n"},
		{name: "Hermes initializer drift", relative: "landing/hermes-agent/plugins/demo/__init__.py", old: "return None", replacement: "pass"},
		{name: "Codex marketplace drift", relative: ".agents/plugins/marketplace.json", old: `"path":"./landing/codex/plugins/demo"`, replacement: `"path":"../demo"`},
		{name: "Codex marketplace legacy source", relative: ".agents/plugins/marketplace.json", old: `"path":"./landing/codex/plugins/demo"`, replacement: `"path":"./plugins/demo"`},
		{name: "Claude marketplace drift", relative: ".claude-plugin/marketplace.json", old: `"owner":{"name":"APM"}`, replacement: `"owner":{"name":"Other"}`},
		{name: "Claude marketplace legacy source", relative: ".claude-plugin/marketplace.json", old: `"source":"./landing/claude-code/plugins/demo"`, replacement: `"source":"./plugins/demo"`},
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

func TestValidateSharedRejectsLegacyLayoutWithoutLanding(t *testing.T) {
	// 捕获错误变更：旧共享布局被静默当作有效 artifact，使 Agent 安装到无落盘内容或含其他 Agent 配置的目录。
	root, ref := writeValidSharedArtifact(t)
	legacy := map[string]string{
		"plugins/demo/.codex-plugin/plugin.json":  `{"name":"demo","version":"1.2.3","description":"Canonical description","skills":"./skills/"}`,
		"plugins/demo/.claude-plugin/plugin.json": `{"name":"demo","version":"1.2.3","description":"Canonical description","skills":["./skills/"]}`,
		"plugins/demo/plugin.yaml":                "schema_version: 1\nname: demo\nversion: 1.2.3\ndescription: Canonical description\n",
		"plugins/demo/__init__.py":                "def register(ctx):\n    return None\n",
	}
	for relative, contents := range legacy {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{"landing", ".agents/plugins/marketplace.json", ".claude-plugin/marketplace.json"} {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	err := pluginartifact.ValidateShared(ref, root)
	if !errors.Is(err, pluginartifact.ErrInvalid) {
		t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), `"landing"`) {
		t.Fatalf("ValidateShared() error = %v, want actionable missing landing hint", err)
	}
}

func TestValidateMarketplaceRequiresCompleteSharedArtifact(t *testing.T) {
	// 捕获错误变更：recovery 只校验目标 Agent，忽略共享 artifact 中另一份派生配置损坏。
	root, ref := writeValidSharedArtifact(t)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash("landing/claude-code/plugins/demo/.claude-plugin/plugin.json"))); err != nil {
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

func TestLandingPathHelpersRejectUnknownAgent(t *testing.T) {
	// 捕获错误变更：未知 Agent 被拼成落盘路径，安装时复制到未定义的目录。
	root := t.TempDir()
	for _, agent := range []domain.AgentID{domain.AgentID("unknown"), domain.AgentID(""), domain.AgentID("Codex")} {
		t.Run(string(agent), func(t *testing.T) {
			if _, err := pluginartifact.LandingRelativeDir(agent); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LandingRelativeDir(%q) error = %v, want ErrInvalid", agent, err)
			}
			if _, err := pluginartifact.LandingPluginPath(root, agent, "demo"); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LandingPluginPath(%q) error = %v, want ErrInvalid", agent, err)
			}
			if _, err := pluginartifact.LandingMarketplaceSource(agent, "demo"); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LandingMarketplaceSource(%q) error = %v, want ErrInvalid", agent, err)
			}
		})
	}
	for _, name := range []string{"", ".", "..", "a/b", "../escape"} {
		t.Run("name "+name, func(t *testing.T) {
			if _, err := pluginartifact.LandingPluginPath(root, domain.AgentBibit, name); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LandingPluginPath(name=%q) error = %v, want ErrInvalid", name, err)
			}
			if _, err := pluginartifact.LandingMarketplaceSource(domain.AgentBibit, name); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("LandingMarketplaceSource(name=%q) error = %v, want ErrInvalid", name, err)
			}
		})
	}
}

func TestLandingPathHelpersUseStableAgentDirectories(t *testing.T) {
	// 捕获错误变更：落盘目录名或 marketplace source 拼写漂移，破坏已发布 cache 的可复用性。
	want := map[domain.AgentID]string{
		domain.AgentCodex:      "codex",
		domain.AgentClaudeCode: "claude-code",
		domain.AgentHermes:     "hermes-agent",
		domain.AgentBibit:      "bibit",
	}
	for agent, directory := range want {
		relative, err := pluginartifact.LandingRelativeDir(agent)
		if err != nil {
			t.Fatalf("LandingRelativeDir(%q) error = %v", agent, err)
		}
		if wantRelative := "landing/" + directory; relative != wantRelative {
			t.Fatalf("LandingRelativeDir(%q) = %q, want %q", agent, relative, wantRelative)
		}
		source, err := pluginartifact.LandingMarketplaceSource(agent, "demo")
		if err != nil {
			t.Fatalf("LandingMarketplaceSource(%q) error = %v", agent, err)
		}
		if wantSource := "./landing/" + directory + "/plugins/demo"; source != wantSource {
			t.Fatalf("LandingMarketplaceSource(%q) = %q, want %q", agent, source, wantSource)
		}
	}
	if got, want := len(pluginartifact.PluginLandingAgents()), len(want); got != want {
		t.Fatalf("PluginLandingAgents() count = %d, want %d", got, want)
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
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "landing", "bibit", "escape")); err != nil {
			t.Fatal(err)
		}
		if err := pluginartifact.ValidateShared(ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
			t.Fatalf("ValidateShared() error = %v, want ErrInvalid", err)
		}
	})

	t.Run("landing skill symlink to canonical", func(t *testing.T) {
		// 捕获错误变更：用 symlink 复用 canonical Skill，使落盘目录依赖 artifact 内其他路径。
		root, ref := writeValidSharedArtifact(t)
		link := filepath.Join(root, "landing", "bibit", "plugins", "demo", "skills", "demo", "SKILL.md")
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "plugins", "demo", "skills", "demo", "SKILL.md"), link); err != nil {
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
	canonicalManifest := `{"schema_version":1,"name":"demo","version":"v1.2.3","description":"Canonical description","skills":["skills/demo"]}`
	codexManifest := `{"name":"demo","version":"1.2.3","description":"Canonical description","skills":"./skills/"}`
	claudeManifest := `{"name":"demo","version":"1.2.3","description":"Canonical description","skills":["./skills/"]}`
	// 派生 plugin.yaml 不再包含 author：Hermes 与 Bibit 读取同一份精确 schema。
	pluginYAML := "schema_version: 1\nname: demo\nversion: 1.2.3\ndescription: Canonical description\n"
	initFile := "def register(ctx):\n    return None\n"
	skillFiles := map[string]string{
		"skills/demo/SKILL.md":      "# Demo\n",
		"skills/demo/skill-card.md": "# Skill Card\n",
	}
	files := map[string]string{
		"plugins/demo/apc-plugin.json": canonicalManifest,
	}
	for relative, contents := range skillFiles {
		files["plugins/demo/"+relative] = contents
	}
	landings := map[domain.AgentID]map[string]string{
		domain.AgentCodex: {
			".codex-plugin/plugin.json": codexManifest,
		},
		domain.AgentClaudeCode: {
			".claude-plugin/plugin.json": claudeManifest,
		},
		domain.AgentHermes: {
			"plugin.yaml": pluginYAML,
			"__init__.py": initFile,
		},
		domain.AgentBibit: {
			"plugin.yaml": pluginYAML,
		},
	}
	for agent, extra := range landings {
		landing, err := pluginartifact.LandingPluginPath(root, agent, "demo")
		if err != nil {
			t.Fatal(err)
		}
		relativeLanding, err := filepath.Rel(root, landing)
		if err != nil {
			t.Fatal(err)
		}
		for relative, contents := range skillFiles {
			files[filepath.ToSlash(filepath.Join(relativeLanding, relative))] = contents
		}
		for relative, contents := range extra {
			files[filepath.ToSlash(filepath.Join(relativeLanding, relative))] = contents
		}
	}
	files[".agents/plugins/marketplace.json"] = `{"name":"apm-demo","interface":{"displayName":"demo"},"plugins":[{"name":"demo","source":{"source":"local","path":"./landing/codex/plugins/demo"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Coding"}]}`
	files[".claude-plugin/marketplace.json"] = `{"name":"apm-demo","owner":{"name":"APM"},"metadata":{"description":"Canonical description","version":"1.2.3"},"plugins":[{"name":"demo","source":"./landing/claude-code/plugins/demo","description":"Canonical description"}]}`
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
