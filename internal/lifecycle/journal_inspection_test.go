package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/transaction"
)

type fakeInspector struct {
	calls  *[]string
	result transaction.InspectionResult
	err    error
}

func (f *fakeInspector) Inspect(context.Context) (transaction.InspectionResult, error) {
	if f.calls != nil {
		*f.calls = append(*f.calls, "inspect")
	}
	return f.result, f.err
}

func TestInspectJournalWarnsRecoveryRequiredAndContinues(t *testing.T) {
	var calls []string
	var stdout, stderr bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(previous) })

	inspectJournal(context.Background(), "search", &fakeInspector{calls: &calls, result: transaction.InspectionResult{
		Active: true, Operation: transaction.OperationInstall, Agent: domain.AgentCodex,
		Power:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		OriginalPhase: transaction.PhasePublishing,
	}})

	if want := []string{"inspect"}; len(calls) != 1 || calls[0] != want[0] {
		t.Fatalf("Inspect calls = %v, want %v", calls, want)
	}
	for _, want := range []string{
		"level=WARN", "msg=\"transaction recovery required\"", "command=search",
		"operation=install", "agent=codex", "power=demo", "version=v1.0.0", "phase=publishing", "hint=\"run apm fix\"",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestInspectJournalWritesVerboseNoJournalDebug(t *testing.T) {
	var stdout, stderr bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, true))
	t.Cleanup(func() { slog.SetDefault(previous) })

	inspectJournal(context.Background(), "search", &fakeInspector{})

	for _, want := range []string{"level=DEBUG", "msg=\"no active transaction before read-only command\"", "command=search"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestInspectJournalWarnsDamagedJournalWithoutFixHint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(logging.NewLogger(&stdout, &stderr, false))
	t.Cleanup(func() { slog.SetDefault(previous) })
	inspectErr := errors.New("journal checksum failure")

	inspectJournal(context.Background(), "search", &fakeInspector{err: inspectErr})

	for _, want := range []string{"level=WARN", "msg=\"transaction journal is damaged\"", "command=search", inspectErr.Error()} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
	if strings.Contains(stderr.String(), "run apm fix") {
		t.Fatalf("stderr = %q, must not offer a fix hint for a damaged journal", stderr.String())
	}
}
