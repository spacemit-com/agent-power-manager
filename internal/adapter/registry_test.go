package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"apm/internal/domain"
)

func TestRegistryResolvesBuiltInSkillAdaptersAndRefreshesInstalledAgentsInConfigOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	for _, root := range []string{".codex", ".hermes"} {
		if err := os.Mkdir(filepath.Join(home, root), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	registry := NewRegistry()
	for _, agent := range []domain.AgentID{
		domain.AgentCodex,
		domain.AgentClaudeCode,
		domain.AgentHermes,
	} {
		got, err := registry.Resolve(agent, domain.PowerTypeSkill)
		if err != nil || got == nil {
			t.Fatalf("Resolve(%q, skill) = %v, %v", agent, got, err)
		}
	}

	snapshot, err := registry.RefreshInstalled()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.AgentID{domain.AgentCodex, domain.AgentHermes}
	if !slices.Equal(snapshot.Ordered(), want) {
		t.Fatalf("snapshot = %v, want %v", snapshot.Ordered(), want)
	}
}

func TestRegistryResolveRejectsUnsupportedAgentAndPower(t *testing.T) {
	registry := NewRegistry()
	for _, request := range []struct {
		agent domain.AgentID
		power domain.PowerType
	}{
		{agent: "unknown", power: domain.PowerTypeSkill},
		{agent: domain.AgentCodex, power: domain.PowerTypePlugin},
		{agent: domain.AgentCodex, power: domain.PowerTypeMCP},
	} {
		got, err := registry.Resolve(request.agent, request.power)
		if got != nil || !errors.Is(err, ErrUnsupportedAdapter) {
			t.Fatalf("Resolve(%q, %q) = %v, %v; want nil, ErrUnsupportedAdapter", request.agent, request.power, got, err)
		}
	}
}

func TestRegistryRefreshInstalledReturnsZeroSnapshotOnDetectionError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".hermes"), 0o755); err != nil {
		t.Fatal(err)
	}

	snapshot, err := NewRegistry().RefreshInstalled()
	if !errors.Is(err, ErrAgentNotInstalled) {
		t.Fatalf("RefreshInstalled() error = %v, want ErrAgentNotInstalled", err)
	}
	if got := snapshot.Ordered(); len(got) != 0 {
		t.Fatalf("RefreshInstalled() snapshot = %v, want zero snapshot", got)
	}
}
