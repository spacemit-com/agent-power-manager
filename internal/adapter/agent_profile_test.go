package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestAgentProfileRootPath(t *testing.T) {
	tests := []struct {
		name, env, defaultDir string
		agent                 domain.AgentID
	}{
		{"codex", "CODEX_HOME", ".codex", domain.AgentCodex},
		{"claude", "CLAUDE_CONFIG_DIR", ".claude", domain.AgentClaudeCode},
		{"hermes", "HERMES_HOME", ".hermes", domain.AgentHermes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := configForAgent(t, tt.agent)
			home := t.TempDir()
			override := filepath.Join(t.TempDir(), "agent-root")
			t.Setenv("APM_HOME", home)
			t.Setenv(tt.env, " \t"+override+"\n")

			got, err := config.rootPath()
			if err != nil {
				t.Fatalf("rootPath() error = %v", err)
			}
			if got != override {
				t.Fatalf("rootPath() = %q, want trimmed override %q", got, override)
			}

			t.Setenv(tt.env, " \t\n")
			got, err = config.rootPath()
			if err != nil {
				t.Fatalf("rootPath() fallback error = %v", err)
			}
			want := filepath.Join(home, tt.defaultDir)
			if got != want {
				t.Fatalf("rootPath() fallback = %q, want %q", got, want)
			}
		})
	}
}

func TestAgentProfileDetectInstalled(t *testing.T) {
	tests := []struct {
		name, env, defaultDir string
		agent                 domain.AgentID
	}{
		{"codex", "CODEX_HOME", ".codex", domain.AgentCodex},
		{"claude", "CLAUDE_CONFIG_DIR", ".claude", domain.AgentClaudeCode},
		{"hermes", "HERMES_HOME", ".hermes", domain.AgentHermes},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := configForAgent(t, tt.agent)

			t.Run("missing root", func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "missing")
				t.Setenv(tt.env, root)

				got, err := config.detectInstalled()
				if err != nil || got {
					t.Fatalf("detectInstalled() = %t, %v; want false, nil", got, err)
				}
			})

			t.Run("directory without skills", func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "agent-root")
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv(tt.env, root)

				got, err := config.detectInstalled()
				if err != nil || !got {
					t.Fatalf("detectInstalled() = %t, %v; want true, nil", got, err)
				}
				if _, err := os.Stat(filepath.Join(root, "skills")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("skills directory changed: Stat error = %v, want not exist", err)
				}
			})

			t.Run("symlink to directory", func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "agent-root")
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(t.TempDir(), "agent-link")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				t.Setenv(tt.env, link)

				got, err := config.detectInstalled()
				if err != nil || !got {
					t.Fatalf("detectInstalled() = %t, %v; want true, nil", got, err)
				}
			})

			t.Run("regular file", func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "agent-root")
				if err := os.WriteFile(root, []byte("not a directory"), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Setenv(tt.env, root)

				got, err := config.detectInstalled()
				if got || !errors.Is(err, ErrAgentNotInstalled) {
					t.Fatalf("detectInstalled() = %t, %v; want false, ErrAgentNotInstalled", got, err)
				}
			})
		})
	}
}

func TestAgentProfileSkillTargetPath(t *testing.T) {
	config := configForAgent(t, domain.AgentCodex)
	root := filepath.Join(t.TempDir(), "agent-root")
	t.Setenv("CODEX_HOME", root)

	got, err := config.skillTargetPath(domain.PowerRef{
		Name: "demo",
		Type: domain.PowerTypeSkill,
	})
	if err != nil {
		t.Fatalf("targetPath() error = %v", err)
	}
	want := filepath.Join(root, "skills", "demo")
	if got != want {
		t.Fatalf("targetPath() = %q, want %q", got, want)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("targetPath() mutated filesystem: Stat(%q) error = %v, want not exist", root, err)
	}

	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypePlugin},
		{Name: "", Type: domain.PowerTypeSkill},
		{Name: " \t", Type: domain.PowerTypeSkill},
		{Name: filepath.Join(t.TempDir(), "demo"), Type: domain.PowerTypeSkill},
		{Name: ".", Type: domain.PowerTypeSkill},
		{Name: "..", Type: domain.PowerTypeSkill},
		{Name: "nested/demo", Type: domain.PowerTypeSkill},
		{Name: `nested\demo`, Type: domain.PowerTypeSkill},
	} {
		if _, err := config.skillTargetPath(ref); !errors.Is(err, ErrInvalidSkill) {
			t.Fatalf("targetPath(%+v) error = %v, want ErrInvalidSkill", ref, err)
		}
	}
}

func TestPluginTargetPath(t *testing.T) {
	config := configForAgent(t, domain.AgentHermes)
	root := filepath.Join(t.TempDir(), "agent-root")
	t.Setenv("HERMES_HOME", root)
	got, err := config.pluginTargetPath("demo-plugin-v0.0.1")
	if err != nil || got != filepath.Join(root, "plugins", "demo-plugin-v0.0.1") {
		t.Fatalf("pluginTargetPath() = %q, %v", got, err)
	}
	for _, name := range []string{"", ".", "..", "nested/demo", `nested\demo`} {
		if _, err := config.pluginTargetPath(name); !errors.Is(err, ErrInvalidPlugin) {
			t.Fatalf("pluginTargetPath(%q) error = %v", name, err)
		}
	}
}

func configForAgent(t *testing.T, agent domain.AgentID) agentProfile {
	t.Helper()
	for _, registration := range builtInAgentRegistrations {
		if registration.profile.id == agent {
			return registration.profile
		}
	}
	t.Fatalf("missing configuration for agent %q", agent)
	return agentProfile{}
}
