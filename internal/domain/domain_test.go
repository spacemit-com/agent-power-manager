package domain_test

import (
	"testing"

	"apm/internal/domain"
)

func TestAgentIDsAreStable(t *testing.T) {
	if domain.AgentCodex != "codex" || domain.AgentClaudeCode != "claude-code" || domain.AgentHermes != "hermes-agent" {
		t.Fatalf("agent IDs = %q, %q, %q", domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes)
	}
}

func TestAgentSnapshotContainsAndOrderedCopy(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{
		domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes,
	}}
	if !snapshot.Contains(domain.AgentClaudeCode) || snapshot.Contains("unknown") {
		t.Fatal("Contains returned an incorrect result")
	}
	got := snapshot.Ordered()
	got[0] = "changed"
	if snapshot.Agents[0] != domain.AgentCodex {
		t.Fatal("Ordered exposed snapshot storage")
	}
}

func TestSharedTypesCompile(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	groups := domain.PowerGroups{domain.PowerTypeSkill: {ref}}
	if groups[domain.PowerTypeSkill][0] != ref {
		t.Fatal("共享类型未保存输入")
	}
}

func TestPowerGroupsKeepTypedVersionedReferences(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	groups := domain.PowerGroups{
		domain.PowerTypeSkill:  {ref},
		domain.PowerTypePlugin: {},
		domain.PowerTypeMCP:    {},
	}
	if got := groups[domain.PowerTypeSkill][0]; got != ref {
		t.Fatalf("skill group = %#v, want %#v", got, ref)
	}
}
