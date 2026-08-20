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

func TestValidateSharedAcceptsBothGeneratedMarketplaces(t *testing.T) {
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

func TestValidateMarketplaceRejectsRequestedSemanticCorruption(t *testing.T) {
	tests := []struct {
		name        string
		agent       domain.AgentID
		relative    string
		old         string
		replacement string
	}{
		{name: "Codex marketplace name", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"name":"apm-demo"`, replacement: `"name":"apm-other"`},
		{name: "Codex Plugin name", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"plugins":[{"name":"demo"`, replacement: `"plugins":[{"name":"other"`},
		{name: "Codex source kind", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"source":"local"`, replacement: `"source":"remote"`},
		{name: "Codex source path", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"path":"./plugins/demo"`, replacement: `"path":"../demo"`},
		{name: "Codex installation policy", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"installation":"AVAILABLE"`, replacement: `"installation":"BLOCKED"`},
		{name: "Codex authentication policy", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"authentication":"ON_INSTALL"`, replacement: `"authentication":"NONE"`},
		{name: "Codex category metadata", agent: domain.AgentCodex, relative: ".agents/plugins/marketplace.json", old: `"category":"Productivity"`, replacement: `"category":"Coding"`},
		{name: "Claude marketplace name", agent: domain.AgentClaudeCode, relative: ".claude-plugin/marketplace.json", old: `"name":"apm-demo"`, replacement: `"name":"apm-other"`},
		{name: "Claude owner metadata", agent: domain.AgentClaudeCode, relative: ".claude-plugin/marketplace.json", old: `"owner":{"name":"Claude Author"}`, replacement: `"owner":{"name":"Other"}`},
		{name: "Claude version metadata", agent: domain.AgentClaudeCode, relative: ".claude-plugin/marketplace.json", old: `"version":"1.2.3"`, replacement: `"version":"9.9.9"`},
		{name: "Claude Plugin name", agent: domain.AgentClaudeCode, relative: ".claude-plugin/marketplace.json", old: `"plugins":[{"name":"demo"`, replacement: `"plugins":[{"name":"other"`},
		{name: "Claude source path", agent: domain.AgentClaudeCode, relative: ".claude-plugin/marketplace.json", old: `"source":"./plugins/demo"`, replacement: `"source":"../demo"`},
		{name: "Claude description metadata", agent: domain.AgentClaudeCode, relative: ".claude-plugin/marketplace.json", old: `"description":"Claude description"`, replacement: `"description":"Other"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			replaceArtifactText(t, filepath.Join(root, test.relative), test.old, test.replacement)
			if err := pluginartifact.ValidateMarketplace(test.agent, ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateMarketplace(%q) error = %v, want ErrInvalid", test.agent, err)
			}
		})
	}
}

func TestValidateMarketplaceReadsOnlyRequestedNativeManifest(t *testing.T) {
	root, ref := writeValidSharedArtifact(t)
	if err := os.Remove(filepath.Join(root, "plugins", ref.Name, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	replaceArtifactText(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), `"version":"1.2.3"`, `"version":"broken"`)
	if err := pluginartifact.ValidateMarketplace(domain.AgentCodex, ref, root); err != nil {
		t.Fatalf("Codex validation read unrelated Claude semantics: %v", err)
	}
}

func TestValidateMarketplaceRejectsRequestedNativeManifestIdentityMismatch(t *testing.T) {
	tests := []struct {
		name        string
		agent       domain.AgentID
		relative    string
		old         string
		replacement string
	}{
		{name: "Codex name", agent: domain.AgentCodex, relative: "plugins/demo/.codex-plugin/plugin.json", old: `"name":"demo"`, replacement: `"name":"other"`},
		{name: "Codex version", agent: domain.AgentCodex, relative: "plugins/demo/.codex-plugin/plugin.json", old: `"version":"1.2.3"`, replacement: `"version":"9.9.9"`},
		{name: "Claude name", agent: domain.AgentClaudeCode, relative: "plugins/demo/.claude-plugin/plugin.json", old: `"name":"demo"`, replacement: `"name":"other"`},
		{name: "Claude version", agent: domain.AgentClaudeCode, relative: "plugins/demo/.claude-plugin/plugin.json", old: `"version":"1.2.3"`, replacement: `"version":"9.9.9"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, ref := writeValidSharedArtifact(t)
			replaceArtifactText(t, filepath.Join(root, filepath.FromSlash(test.relative)), test.old, test.replacement)
			if err := pluginartifact.ValidateMarketplace(test.agent, ref, root); !errors.Is(err, pluginartifact.ErrInvalid) {
				t.Fatalf("ValidateMarketplace(%q) error = %v, want ErrInvalid", test.agent, err)
			}
		})
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
		"plugins/demo/.codex-plugin/plugin.json":  `{"name":"demo","version":"1.2.3","description":"Codex description","author":{"name":"Codex Author"},"interface":{"displayName":"Demo Codex","category":"Productivity"}}`,
		"plugins/demo/.claude-plugin/plugin.json": `{"name":"demo","version":"1.2.3","description":"Claude description","author":{"name":"Claude Author"}}`,
		".agents/plugins/marketplace.json":        `{"name":"apm-demo","interface":{"displayName":"Demo Codex"},"plugins":[{"name":"demo","source":{"source":"local","path":"./plugins/demo"},"policy":{"installation":"AVAILABLE","authentication":"ON_INSTALL"},"category":"Productivity"}]}`,
		".claude-plugin/marketplace.json":         `{"name":"apm-demo","owner":{"name":"Claude Author"},"metadata":{"description":"Claude description","version":"1.2.3"},"plugins":[{"name":"demo","source":"./plugins/demo","description":"Claude description"}]}`,
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
