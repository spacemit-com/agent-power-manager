package lifecycle

import (
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
