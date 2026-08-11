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

type recordingDetectAdapter struct {
	agent domain.AgentID
	calls *[]domain.AgentID
	found bool
	err   error
}

func (a recordingDetectAdapter) DetectAgent() (bool, error) {
	*a.calls = append(*a.calls, a.agent)
	return a.found, a.err
}

func (recordingDetectAdapter) TargetPath(domain.PowerRef) (string, error) { return "", nil }

func (recordingDetectAdapter) Install(domain.PowerRef, string) (InstallResult, error) {
	return InstallResult{}, nil
}

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

func TestRegistryRefreshInstalledSortsDetectionAndSnapshotByAgentID(t *testing.T) {
	var calls []domain.AgentID
	registration := []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode}
	registry := &Registry{
		ordered: slices.Clone(registration),
		byAgent: map[domain.AgentID]Adapter{},
	}
	for _, agent := range registry.ordered {
		registry.byAgent[agent] = recordingDetectAdapter{agent: agent, calls: &calls, found: agent != domain.AgentCodex}
	}

	snapshot, err := registry.RefreshInstalled()
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}
	wantSnapshot := []domain.AgentID{domain.AgentClaudeCode, domain.AgentHermes}
	if !slices.Equal(calls, wantCalls) || !slices.Equal(snapshot.Ordered(), wantSnapshot) {
		t.Fatalf("calls/snapshot = %v/%v, want %v/%v", calls, snapshot.Ordered(), wantCalls, wantSnapshot)
	}
	if !slices.Equal(registry.ordered, registration) {
		t.Fatalf("registration order = %v, want unchanged %v", registry.ordered, registration)
	}
}

func TestRegistryRefreshInstalledStopsAfterFirstDetectionError(t *testing.T) {
	var calls []domain.AgentID
	detectErr := errors.New("permission denied")
	registry := &Registry{
		ordered: []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode},
		byAgent: map[domain.AgentID]Adapter{},
	}
	for _, agent := range registry.ordered {
		adapter := recordingDetectAdapter{agent: agent, calls: &calls, found: true}
		if agent == domain.AgentClaudeCode {
			adapter.err = detectErr
		}
		registry.byAgent[agent] = adapter
	}

	snapshot, err := registry.RefreshInstalled()
	if !slices.Equal(calls, []domain.AgentID{domain.AgentClaudeCode}) {
		t.Fatalf("DetectAgent calls = %v, want only %q", calls, domain.AgentClaudeCode)
	}
	if snapshot.Agents != nil {
		t.Fatalf("snapshot = %#v, want zero snapshot", snapshot)
	}
	if !errors.Is(err, detectErr) || !strings.Contains(err.Error(), string(domain.AgentClaudeCode)) {
		t.Fatalf("error = %v, want agent %q wrapping %v", err, domain.AgentClaudeCode, detectErr)
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
