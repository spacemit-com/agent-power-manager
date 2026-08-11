package cli

import (
	"errors"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestRenderInstallWritesExactOutcomeLines(t *testing.T) {
	result := InstallResult{Items: []InstallItem{
		{Agent: domain.AgentClaudeCode, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: InstallOutcomeUpgraded},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: InstallOutcomeDowngraded},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "same", Version: "v1.0.0"}, Outcome: InstallOutcomeUnchanged},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bad", Version: "v1.0.0"}, Outcome: InstallOutcomeFailed},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "later", Version: "v1.0.0"}, Outcome: InstallOutcomeUnexecuted},
	}}
	var output strings.Builder
	if err := renderInstall(&output, result); err != nil {
		t.Fatal(err)
	}
	want := "claude-code/skill/demo@v2.0.0 installed\n" +
		"codex/skill/demo@v2.0.0 upgraded\n" +
		"hermes-agent/skill/demo@v2.0.0 downgraded\n" +
		"codex/skill/same@v1.0.0 unchanged\n" +
		"codex/skill/bad@v1.0.0 failed\n" +
		"hermes-agent/skill/later@v1.0.0 unexecuted\n"
	if output.String() != want {
		t.Fatalf("renderInstall() = %q, want %q", output.String(), want)
	}
}

func TestInstallBatchErrorUnwrapPreservesCause(t *testing.T) {
	cause := errors.New("manifest write failed")
	err := &InstallBatchError{Agent: domain.AgentCodex, Err: cause}

	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false, want true", err)
	}
	if got, want := err.Error(), `install power for agent "codex" failed: manifest write failed`; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestRenderInstallWrapsSecondWriteFailureAndPreservesFirstLine(t *testing.T) {
	cause := errors.New("disk full")
	writer := &installFailOnWrite{failAt: 2, err: cause}
	result := InstallResult{Items: []InstallItem{
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}, Outcome: InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"}, Outcome: InstallOutcomeUpgraded},
	}}

	err := renderInstall(writer, result)
	if err == nil || !strings.Contains(err.Error(), "write install result") {
		t.Fatalf("renderInstall() error = %v, want wrapped write install result error", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("renderInstall() error = %v, want cause %v", err, cause)
	}
	if got, want := writer.output.String(), "codex/skill/first@v1.0.0 installed\n"; got != want {
		t.Fatalf("renderInstall() output = %q, want completed prefix %q", got, want)
	}
}

type installFailOnWrite struct {
	output strings.Builder
	failAt int
	writes int
	err    error
}

func (w *installFailOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.output.Write(p)
}
