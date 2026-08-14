package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"apm/internal/domain"
)

func TestRegistryResolvesBuiltInSkillAdaptersAndRefreshesInstalledAgentsInAgentIDOrder(t *testing.T) {
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
		got, err := registry.ResolveSkill(agent)
		if err != nil || got == nil {
			t.Fatalf("ResolveSkill(%q) = %v, %v", agent, got, err)
		}
		mcp, err := registry.ResolveMCP(agent)
		if err != nil || mcp == nil {
			t.Fatalf("ResolveMCP(%q) = %v, %v", agent, mcp, err)
		}
		plugin, err := registry.ResolvePlugin(agent)
		if err != nil || plugin == nil {
			t.Fatalf("ResolvePlugin(%q) = %v, %v", agent, plugin, err)
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

func TestRegistryRefreshInstalledSortsSnapshotByAgentID(t *testing.T) {
	root := t.TempDir()
	registration := []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode}
	profiles := make(map[domain.AgentID]agentProfile, len(registration))
	for _, agent := range registration {
		homeEnv := "TEST_" + string(agent) + "_HOME"
		agentRoot := filepath.Join(root, string(agent))
		t.Setenv(homeEnv, agentRoot)
		if agent != domain.AgentCodex {
			if err := os.Mkdir(agentRoot, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		profiles[agent] = agentProfile{id: agent, homeEnv: homeEnv}
	}
	registry := &Registry{
		ordered:  slices.Clone(registration),
		profiles: profiles,
	}

	snapshot, err := registry.RefreshInstalled()
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot := []domain.AgentID{domain.AgentClaudeCode, domain.AgentHermes}
	if !slices.Equal(snapshot.Ordered(), wantSnapshot) {
		t.Fatalf("snapshot = %v, want %v", snapshot.Ordered(), wantSnapshot)
	}
	if !slices.Equal(registry.ordered, registration) {
		t.Fatalf("registration order = %v, want unchanged %v", registry.ordered, registration)
	}
}

func TestRegistryRefreshInstalledStopsAfterFirstDetectionError(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	if err := os.WriteFile(claudeRoot, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_CLAUDE_HOME", claudeRoot)
	registry := &Registry{
		ordered: []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode},
		profiles: map[domain.AgentID]agentProfile{
			domain.AgentClaudeCode: {id: domain.AgentClaudeCode, homeEnv: "TEST_CLAUDE_HOME"},
			domain.AgentCodex:      {id: domain.AgentCodex, homeEnv: "TEST_CODEX_HOME"},
			domain.AgentHermes:     {id: domain.AgentHermes, homeEnv: "TEST_HERMES_HOME"},
		},
	}

	snapshot, err := registry.RefreshInstalled()
	if snapshot.Agents != nil {
		t.Fatalf("snapshot = %#v, want zero snapshot", snapshot)
	}
	if !errors.Is(err, ErrAgentNotInstalled) {
		t.Fatalf("error = %v, want ErrAgentNotInstalled", err)
	}
}

func TestRegistryResolversRejectUnsupportedAgent(t *testing.T) {
	registry := NewRegistry()
	if got, err := registry.ResolveSkill("unknown"); got != nil || !errors.Is(err, ErrUnsupportedAdapter) {
		t.Fatalf("ResolveSkill(unknown) = %v, %v; want nil, ErrUnsupportedAdapter", got, err)
	}
	if got, err := registry.ResolveMCP("unknown"); got != nil || !errors.Is(err, ErrUnsupportedAdapter) {
		t.Fatalf("ResolveMCP(unknown) = %v, %v; want nil, ErrUnsupportedAdapter", got, err)
	}
	if got, err := registry.ResolvePlugin("unknown"); got != nil || !errors.Is(err, ErrUnsupportedAdapter) {
		t.Fatalf("ResolvePlugin(unknown) = %v, %v; want nil, ErrUnsupportedAdapter", got, err)
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
