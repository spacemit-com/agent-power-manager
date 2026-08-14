package adapter

import (
	"errors"
	"testing"

	"apm/internal/domain"
)

func TestValidatePluginFactRejectsContradictoryState(t *testing.T) {
	for _, fact := range []PluginFact{
		{Presence: PluginAbsent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"},
		{Presence: PluginPresent, ActualName: "demo-v1.0.0"},
		{Presence: "unknown", ActualName: "demo-v1.0.0"},
		{Presence: PluginPresent, ActualName: "../demo", Selector: "demo@apm"},
	} {
		if err := validatePluginFact(fact); !errors.Is(err, ErrInvalidPlugin) {
			t.Fatalf("validatePluginFact(%#v) error = %v, want ErrInvalidPlugin", fact, err)
		}
	}
	if err := validatePluginFact(PluginFact{Presence: PluginAbsent, ActualName: "demo-v1.0.0"}); err != nil {
		t.Fatalf("validate absent fact: %v", err)
	}
	if err := validatePluginFact(PluginFact{Presence: PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"}); err != nil {
		t.Fatalf("validate present fact: %v", err)
	}
}

func TestValidatePluginTargetAndDeployment(t *testing.T) {
	valid := PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"}
	if err := validatePluginTarget(valid); err != nil {
		t.Fatalf("validate target: %v", err)
	}
	for _, target := range []PluginTarget{
		{},
		{ActualName: "../demo", Selector: "demo@apm"},
		{ActualName: "demo-v1.0.0"},
	} {
		if err := validatePluginTarget(target); !errors.Is(err, ErrInvalidPlugin) {
			t.Fatalf("validatePluginTarget(%#v) error = %v", target, err)
		}
	}
	for _, mode := range []domain.PluginDeploymentMode{domain.PluginDeploymentMarketplace, domain.PluginDeploymentFilesystem} {
		if err := validatePluginDeployment(mode); err != nil {
			t.Fatalf("validatePluginDeployment(%q): %v", mode, err)
		}
	}
	if err := validatePluginDeployment("unknown"); !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("unknown deployment error = %v", err)
	}
}

func TestPluginMutationFromCommandClassifiesSideEffects(t *testing.T) {
	tests := []struct {
		name      string
		result    agentCLIResult
		runErr    error
		success   bool
		wantState PluginMutationState
		wantErr   error
	}{
		{name: "not started", result: agentCLIResult{}, runErr: ErrAgentCLIUnavailable, wantState: PluginMutationNotApplied, wantErr: ErrAgentCLIUnavailable},
		{name: "applied", result: agentCLIResult{Started: true, ExitCode: 0}, success: true, wantState: PluginMutationApplied},
		{name: "failed after start", result: agentCLIResult{Started: true, ExitCode: 1, Stderr: "failed"}, runErr: errors.New("exit"), wantState: PluginMutationUnknown, wantErr: ErrPluginMutationUnknown},
		{name: "truncated", result: agentCLIResult{Started: true, ExitCode: 0, StdoutTruncated: true}, success: true, wantState: PluginMutationUnknown, wantErr: ErrPluginMutationUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := pluginMutationFromCommand("codex", "install", PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm"}, test.result, test.runErr, test.success)
			if got.State != test.wantState || !errors.Is(err, test.wantErr) {
				t.Fatalf("pluginMutationFromCommand() = %#v, %v; want %q, %v", got, err, test.wantState, test.wantErr)
			}
		})
	}
}
