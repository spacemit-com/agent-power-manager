package cli

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/logging"
)

func TestValidateCommonExpertType(t *testing.T) {
	if err := validateCommon(&Flags{Type: "expert", Agent: ""}); err != nil {
		t.Fatalf("empty agent error = %v, want nil", err)
	}
	if err := validateCommon(&Flags{Type: "expert", Agent: "bibit"}); err != nil {
		t.Fatalf("bibit agent error = %v, want nil", err)
	}
	if err := validateCommon(&Flags{Type: "expert", Agent: "codex"}); err == nil {
		t.Fatal("codex agent error = nil, want error")
	}
	if err := validateCommon(&Flags{Type: "expert", Agent: "all"}); err == nil {
		t.Fatal("all agent error = nil, want error")
	}
}

// The legacy spelling must keep working (an already-installed plugin still sends it) and
// must come out canonical, because the raw value flows on into internal/lifecycle.
func TestResolvePowerTypeNormalizesLegacyAgentSpelling(t *testing.T) {
	quiet := slog.Default()
	t.Cleanup(func() { slog.SetDefault(quiet) })
	slog.SetDefault(logging.NewLogger(io.Discard, io.Discard, false))

	legacy := Flags{Type: "agent"}
	if err := resolvePowerType(&legacy); err != nil {
		t.Fatalf("resolvePowerType(agent) error = %v", err)
	}
	if legacy.Type != "expert" {
		t.Fatalf("legacy spelling normalized to %q, want expert", legacy.Type)
	}
	for _, powerType := range []string{"", "skill", "plugin", "mcp", "expert"} {
		flags := Flags{Type: powerType}
		if err := resolvePowerType(&flags); err != nil {
			t.Fatalf("resolvePowerType(%q) error = %v", powerType, err)
		}
		if flags.Type != powerType {
			t.Fatalf("resolvePowerType(%q) rewrote it to %q", powerType, flags.Type)
		}
	}
	if err := resolvePowerType(&Flags{Type: "bogus"}); err == nil {
		t.Fatal("resolvePowerType(bogus) error = nil, want error")
	}
	if err := resolvePowerType(&Flags{Type: "Agent"}); err == nil {
		t.Fatal("resolvePowerType(Agent) error = nil, want case-sensitive rejection")
	}
}

func TestResolvePowerTypeWarnsOnlyOutsideJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))

	if err := resolvePowerType(&Flags{Type: "agent"}); err != nil {
		t.Fatalf("resolvePowerType(agent) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Fatalf("stderr = %q, want a deprecation warning", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want the warning on stderr only", stdout.String())
	}

	// --json is a machine contract: a nudge is not a result and must not appear.
	stdout.Reset()
	stderr.Reset()
	if err := resolvePowerType(&Flags{Type: "agent", JSON: true}); err != nil {
		t.Fatalf("resolvePowerType(agent, json) error = %v", err)
	}
	if stderr.Len() != 0 || stdout.Len() != 0 {
		t.Fatalf("json run wrote stdout %q / stderr %q, want silence", stdout.String(), stderr.String())
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
	if err := validateCommon(&Flags{Type: "skill", Agent: "foo"}); err == nil {
		t.Fatal("validateCommon() error = nil, want unknown agent")
	}
}

func TestValidateExpertTypeFlagsRejectsUnknownAgent(t *testing.T) {
	if err := validateExpertTypeFlags(&Flags{Type: "expert", Agent: "foo"}); err == nil {
		t.Fatal("validateExpertTypeFlags() error = nil, want unknown agent")
	}
}
