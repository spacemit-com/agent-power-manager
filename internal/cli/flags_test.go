package cli

import "testing"

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
