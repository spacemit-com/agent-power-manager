package domain

import (
	"slices"
	"testing"
)

func TestKnownAgentIDs(t *testing.T) {
	got := KnownAgentIDs()
	want := []AgentID{AllAgents, AgentBibit, AgentClaudeCode, AgentCodex, AgentHermes}
	if !slices.Equal(got, want) {
		t.Fatalf("KnownAgentIDs() = %v, want %v", got, want)
	}
}

func TestIsKnownAgentID(t *testing.T) {
	for _, id := range []AgentID{AllAgents, AgentBibit, AgentClaudeCode, AgentCodex, AgentHermes} {
		if !IsKnownAgentID(id) {
			t.Fatalf("IsKnownAgentID(%q) = false, want true", id)
		}
	}
	for _, id := range []AgentID{"", "foo", "claude", "hermes", "codex ", " codex", "ALL", "bibit "} {
		if IsKnownAgentID(id) {
			t.Fatalf("IsKnownAgentID(%q) = true, want false", id)
		}
	}
}
