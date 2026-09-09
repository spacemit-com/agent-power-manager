package cli

import (
	"strings"
	"testing"
)

func TestValidateCommonAgentType(t *testing.T) {
	if err := validateCommon(Flags{Type: "agent", Agent: ""}); err != nil {
		t.Fatalf("empty agent error = %v, want nil", err)
	}
	if err := validateCommon(Flags{Type: "agent", Agent: "bibit"}); err != nil {
		t.Fatalf("bibit agent error = %v, want nil", err)
	}
	if err := validateCommon(Flags{Type: "agent", Agent: "codex"}); err == nil {
		t.Fatal("codex agent error = nil, want error")
	}
	if err := validateCommon(Flags{Type: "agent", Agent: "all"}); err == nil {
		t.Fatal("all agent error = nil, want error")
	}
}

func TestValidatePowerTypeAcceptsAgent(t *testing.T) {
	if err := validatePowerType(Flags{Type: "agent"}); err != nil {
		t.Fatalf("validatePowerType(agent) error = %v", err)
	}
}

func TestValidateAgentValueAcceptsKnownIDs(t *testing.T) {
	for _, agent := range []string{"", "all", "bibit", "claude-code", "codex", "hermes-agent", " codex "} {
		if err := validateAgentValue(Flags{Agent: agent}); err != nil {
			t.Fatalf("validateAgentValue(%q) error = %v, want nil", agent, err)
		}
	}
}

func TestValidateAgentValueRejectsUnknownIDs(t *testing.T) {
	for _, agent := range []string{"foo", "claude", "hermes", "codex-code", "ALL"} {
		err := validateAgentValue(Flags{Agent: agent})
		if err == nil {
			t.Fatalf("validateAgentValue(%q) error = nil, want error", agent)
		}
		if !strings.Contains(err.Error(), "must be all, bibit, claude-code, codex, or hermes-agent") {
			t.Fatalf("validateAgentValue(%q) error = %v, want option list", agent, err)
		}
	}
}

func TestValidateCommonRejectsUnknownAgent(t *testing.T) {
	if err := validateCommon(Flags{Type: "skill", Agent: "foo"}); err == nil {
		t.Fatal("validateCommon() error = nil, want unknown agent")
	}
}

func TestValidateAgentTypeFlagsRejectsUnknownAgent(t *testing.T) {
	if err := validateAgentTypeFlags(Flags{Type: "agent", Agent: "foo"}); err == nil {
		t.Fatal("validateAgentTypeFlags() error = nil, want unknown agent")
	}
}
