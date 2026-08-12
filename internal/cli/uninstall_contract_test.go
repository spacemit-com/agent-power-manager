package cli

import (
	"errors"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestRenderUninstallWritesExactPowerAndAgentOnlyLines(t *testing.T) {
	completed := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"}
	failed := domain.PowerRef{Type: domain.PowerTypePlugin, Name: "other", Version: "v2.0.0"}
	skipped := domain.PowerRef{Type: domain.PowerTypeMCP, Name: "later", Version: "v3.0.0"}
	result := UninstallResult{Items: []UninstallItem{
		{Agent: domain.AgentClaudeCode, Power: &completed, Outcome: UninstallOutcomeCompleted},
		{Agent: domain.AgentCodex, Power: &failed, Outcome: UninstallOutcomeFailed},
		{Agent: domain.AgentHermes, Power: &skipped, Outcome: UninstallOutcomeUnexecuted},
		{Agent: domain.AgentCodex, Outcome: UninstallOutcomeFailed},
		{Agent: domain.AgentHermes, Outcome: UninstallOutcomeUnexecuted},
	}}
	var output strings.Builder

	if err := renderUninstall(&output, result); err != nil {
		t.Fatal(err)
	}
	want := "claude-code/skill/demo@v1.2.3 completed\n" +
		"codex/plugin/other@v2.0.0 failed\n" +
		"hermes-agent/mcp/later@v3.0.0 unexecuted\n" +
		"codex failed\n" +
		"hermes-agent unexecuted\n"
	if output.String() != want {
		t.Fatalf("renderUninstall() = %q, want %q", output.String(), want)
	}
}

func TestUninstallBatchErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("remove failed")
	err := &UninstallBatchError{Agent: domain.AgentCodex, Err: cause}
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is(%v, cause) = false", err)
	}
	if got, want := err.Error(), `uninstall power for agent "codex" failed: remove failed`; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestRenderUninstallWrapsSecondWriteFailureAndPreservesFirstLine(t *testing.T) {
	cause := errors.New("disk full")
	writer := &uninstallFailOnWrite{failAt: 2, err: cause}
	result := UninstallResult{Items: []UninstallItem{
		{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}, Outcome: UninstallOutcomeCompleted},
		{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"}, Outcome: UninstallOutcomeFailed},
	}}

	err := renderUninstall(writer, result)
	if err == nil || !strings.Contains(err.Error(), "write uninstall result") {
		t.Fatalf("renderUninstall() error = %v, want wrapped write uninstall result error", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("renderUninstall() error = %v, want cause %v", err, cause)
	}
	if got, want := writer.output.String(), "codex/skill/first@v1.0.0 completed\n"; got != want {
		t.Fatalf("renderUninstall() output = %q, want completed prefix %q", got, want)
	}
}

type uninstallFailOnWrite struct {
	output strings.Builder
	failAt int
	writes int
	err    error
}

func (w *uninstallFailOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.output.Write(p)
}
