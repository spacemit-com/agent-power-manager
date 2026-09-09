package lifecycle

import (
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestInstallAgentsDefaultsBibitForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentBibit}}
	agents, err := installAgents(cli.Flags{Type: "agent", Agent: ""}, snapshot)
	if err != nil || len(agents) != 1 || agents[0] != domain.AgentBibit {
		t.Fatalf("installAgents() = %v, %v", agents, err)
	}
}

func TestInstallAgentsRejectsNonBibitForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex, domain.AgentBibit}}
	if _, err := installAgents(cli.Flags{Type: "agent", Agent: "codex"}, snapshot); err == nil {
		t.Fatal("installAgents() error = nil, want unsupported agent")
	}
}

func TestInstalledListAgentsDefaultsBibitForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentBibit}}
	agents, err := installedListAgents(cli.Flags{Type: "agent", Agent: ""}, snapshot)
	if err != nil || len(agents) != 1 || agents[0] != domain.AgentBibit {
		t.Fatalf("installedListAgents() = %v, %v", agents, err)
	}
}

func TestInstalledListAgentsRejectsNonBibitForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex, domain.AgentBibit}}
	if _, err := installedListAgents(cli.Flags{Type: "agent", Agent: "codex"}, snapshot); err == nil {
		t.Fatal("installedListAgents() error = nil, want unsupported agent")
	}
}

func TestInstallAgentsReportsAvailableAgentsWhenTargetMissing(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}
	_, err := installAgents(cli.Flags{Type: "skill", Agent: "hermes-agent"}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "available agents: codex") {
		t.Fatalf("installAgents() error = %v, want available agents: codex", err)
	}
}

func TestUninstallAgentsReportsAvailableAgentsWhenTargetMissing(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}
	_, err := uninstallAgents(cli.Flags{Type: "skill", Agent: "hermes-agent"}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "available agents: codex") {
		t.Fatalf("uninstallAgents() error = %v, want available agents: codex", err)
	}
}

func TestInstalledListAgentsReportsAvailableAgentsWhenTargetMissing(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}
	_, err := installedListAgents(cli.Flags{Type: "skill", Agent: "hermes-agent"}, snapshot)
	if err == nil || !strings.Contains(err.Error(), "available agents: codex") {
		t.Fatalf("installedListAgents() error = %v, want available agents: codex", err)
	}
}

func TestInstallAgentsOmitsAvailableAgentsForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}
	_, err := installAgents(cli.Flags{Type: "agent", Agent: ""}, snapshot)
	if err == nil || strings.Contains(err.Error(), "available agents") {
		t.Fatalf("installAgents() error = %v, want no available agents for agent type", err)
	}
}

func TestInstallAgentsOmitsAvailableAgentsWhenSnapshotEmpty(t *testing.T) {
	_, err := installAgents(cli.Flags{Type: "skill", Agent: "codex"}, domain.AgentSnapshot{})
	if err == nil || strings.Contains(err.Error(), "available agents") {
		t.Fatalf("installAgents() error = %v, want no available agents on empty snapshot", err)
	}
}
