package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	registration := []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode}
	profiles := make(map[domain.AgentID]agentProfile, len(registration))
	for _, agent := range registration {
		profiles[agent] = agentProfile{id: agent}
	}
	var detected []domain.AgentID
	registry := &Registry{
		ordered:  slices.Clone(registration),
		profiles: profiles,
		detectProfile: func(profile agentProfile) (bool, error) {
			detected = append(detected, profile.id)
			return profile.id != domain.AgentCodex, nil
		},
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
	wantDetected := []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}
	if !slices.Equal(detected, wantDetected) {
		t.Fatalf("detection order = %v, want %v", detected, wantDetected)
	}
}

func TestRegistryRefreshInstalledStopsAfterFirstDetectionError(t *testing.T) {
	cause := errors.New("detect failed")
	var detected []domain.AgentID
	registry := &Registry{
		ordered: []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode},
		profiles: map[domain.AgentID]agentProfile{
			domain.AgentClaudeCode: {id: domain.AgentClaudeCode},
			domain.AgentCodex:      {id: domain.AgentCodex},
			domain.AgentHermes:     {id: domain.AgentHermes},
		},
		detectProfile: func(profile agentProfile) (bool, error) {
			detected = append(detected, profile.id)
			if profile.id == domain.AgentClaudeCode {
				return false, cause
			}
			return true, nil
		},
	}

	snapshot, err := registry.RefreshInstalled()
	if snapshot.Agents != nil {
		t.Fatalf("snapshot = %#v, want zero snapshot", snapshot)
	}
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), string(domain.AgentClaudeCode)) {
		t.Fatalf("error = %v, want cause and failing Agent context", err)
	}
	if want := []domain.AgentID{domain.AgentClaudeCode}; !slices.Equal(detected, want) {
		t.Fatalf("detection calls = %v, want stop after %v", detected, want)
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

func TestRegistryPluginMarketplaceCapabilitiesMatchAgentSupport(t *testing.T) {
	registry := NewRegistry()
	for _, test := range []struct {
		agent           domain.AgentID
		wantMarketplace bool
	}{
		{agent: domain.AgentCodex, wantMarketplace: true},
		{agent: domain.AgentClaudeCode, wantMarketplace: true},
		{agent: domain.AgentHermes, wantMarketplace: false},
	} {
		plugin, err := registry.ResolvePlugin(test.agent)
		if err != nil {
			t.Fatalf("ResolvePlugin(%q): %v", test.agent, err)
		}
		_, gotMarketplace := plugin.(MarketplacePluginAdapter)
		if gotMarketplace != test.wantMarketplace {
			t.Fatalf("ResolvePlugin(%q) marketplace capability = %t, want %t", test.agent, gotMarketplace, test.wantMarketplace)
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
