package cli

import (
	"errors"
	"io"
	"strings"
	"testing"

	"apm/internal/domain"
)

func TestRenderListWritesExactCloudAndInstalledLines(t *testing.T) {
	result := ListResult{Powers: []ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "cloud", Version: "v2.0.0"}},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "local", Version: "v1.0.0"}},
	}}
	var output strings.Builder
	if err := renderList(&output, result); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "mcp/cloud@v2.0.0\ncodex/skill/local@v1.0.0\n"; got != want {
		t.Fatalf("renderList() = %q, want %q", got, want)
	}
}

func TestRenderListWritesNothingForEmptyResult(t *testing.T) {
	var output strings.Builder
	if err := renderList(&output, ListResult{}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "" {
		t.Fatalf("renderList() = %q, want empty output", got)
	}
}

func TestRenderListWrapsSecondWriteFailureAndPreservesFirstLine(t *testing.T) {
	cause := errors.New("disk full")
	writer := &failOnWrite{failAt: 2, err: cause}
	result := ListResult{Powers: []ListedPower{
		{Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "first", Version: "v1.0.0"}},
		{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"}},
	}}

	err := renderList(writer, result)
	if err == nil || !strings.Contains(err.Error(), "write list result") {
		t.Fatalf("renderList() error = %v, want wrapped write list result error", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("renderList() error = %v, want cause %v", err, cause)
	}
	if got, want := writer.output.String(), "mcp/first@v1.0.0\n"; got != want {
		t.Fatalf("renderList() output = %q, want completed prefix %q", got, want)
	}
}

func TestPartialListErrorReportsAgentsAndUnwrapsCauses(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	err := &PartialListError{Failures: []AgentFailure{
		{Agent: domain.AgentClaudeCode, Err: first},
		{Agent: domain.AgentCodex, Err: second},
	}}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("missing cause: %v", err)
	}
	if got := err.Error(); !strings.Contains(got, "claude-code") || !strings.Contains(got, "codex") {
		t.Fatalf("Error() = %q, want both agents", got)
	}
}

type failOnWrite struct {
	output strings.Builder
	failAt int
	writes int
	err    error
}

func (w *failOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.output.Write(p)
}

var _ io.Writer = (*failOnWrite)(nil)
