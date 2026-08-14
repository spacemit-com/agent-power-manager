package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/logging"
)

type recordingSearchLifecycle struct {
	flags  cli.Flags
	result cli.SearchResult
	err    error
	calls  int
}

func (l *recordingSearchLifecycle) Search(_ context.Context, flags cli.Flags) (cli.SearchResult, error) {
	l.calls++
	l.flags = flags
	return l.result, l.err
}

type warningSearchLifecycle struct {
	result cli.SearchResult
}

func (l *warningSearchLifecycle) Search(context.Context, cli.Flags) (cli.SearchResult, error) {
	slog.Warn("transaction recovered", slog.String("operation", "install"))
	return l.result, nil
}

func newSearchEntry(t *testing.T, stdout, stderr io.Writer, factory cli.SearchLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, factory, func() (cli.ListLifecycle, error) {
		t.Fatal("Search dispatched to List factory")
		return nil, nil
	}, func() (cli.InstallLifecycle, error) {
		t.Fatal("Search dispatched to Install factory")
		return nil, nil
	}, func() (cli.UninstallLifecycle, error) {
		t.Fatal("Search dispatched to Uninstall factory")
		return nil, nil
	}, func() (cli.FixLifecycle, error) {
		t.Fatal("Search dispatched to Fix factory")
		return nil, nil
	})
}

func TestSearchInvokesLifecycleWithTrimmedKeywordAndType(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSearchLifecycle{}
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"search", "  summary\t", "--type=skill"}); err != nil {
		t.Fatalf("Execute(search) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("Search calls = %d, want 1", lifecycle.calls)
	}
	if lifecycle.flags.Keyword != "summary" || lifecycle.flags.Type != "skill" {
		t.Fatalf("Search flags = %#v, want keyword summary and type skill", lifecycle.flags)
	}
}

func TestSearchRendersExactResultLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSearchLifecycle{result: cli.SearchResult{Powers: []domain.PowerRef{
		{Type: domain.PowerTypeMCP, Name: "alpha", Version: "v2.0.0"},
		{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
	}}}
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"search", "a"}); err != nil {
		t.Fatalf("Execute(search) error = %v", err)
	}
	const want = "mcp/alpha@v2.0.0\nskill/beta@v1.0.0\n"
	if got := stdout.String(); got != want {
		t.Fatalf("search stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("search stderr = %q, want empty", stderr.String())
	}
}

func TestSearchRoutesRecoveryWarnToStderrAndBusinessResultsToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return &warningSearchLifecycle{result: cli.SearchResult{Powers: []domain.PowerRef{
			{Type: domain.PowerTypeSkill, Name: "summary", Version: "v1.2.3"},
		}}}, nil
	})

	if err := entry.Execute([]string{"search", "summary"}); err != nil {
		t.Fatalf("Execute(search) error = %v", err)
	}
	if got, want := stdout.String(), "skill/summary@v1.2.3\n"; got != want {
		t.Fatalf("search stdout = %q, want only business results %q", got, want)
	}
	if got := stderr.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "transaction recovered") {
		t.Fatalf("search stderr = %q, want recovery WARN", got)
	}
	if strings.Contains(stderr.String(), "skill/summary@v1.2.3") {
		t.Fatalf("search stderr = %q, want no business result", stderr.String())
	}
}

func TestSearchEmptyResultWritesNothing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return &recordingSearchLifecycle{}, nil
	})

	if err := entry.Execute([]string{"search", "none"}); err != nil {
		t.Fatalf("Execute(search) error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("search output = stdout %q, stderr %q; want both empty", stdout.String(), stderr.String())
	}
}

func TestSearchRejectsAgentAsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return &recordingSearchLifecycle{}, nil
	})

	err := entry.Execute([]string{"search", "summary", "--agent=all"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --agent") {
		t.Fatalf("Execute(search --agent) error = %v, want unknown agent flag", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("search stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "Usage:") || strings.Count(got, "level=ERROR") != 1 {
		t.Fatalf("search stderr = %q, want one error and usage", got)
	}
}

func TestSearchHelpDoesNotListAgentFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return &recordingSearchLifecycle{}, nil
	})

	if err := entry.Execute([]string{"search", "--help"}); err != nil {
		t.Fatalf("Execute(search --help) error = %v", err)
	}
	if strings.Contains(stdout.String(), "--agent") {
		t.Fatalf("search help = %q, want no agent flag", stdout.String())
	}
}

func TestSearchLifecycleErrorIsLoggedOnceWithUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return &recordingSearchLifecycle{err: errors.New("search failed")}, nil
	})

	err := entry.Execute([]string{"search", "summary"})
	if err == nil || !strings.Contains(err.Error(), "search failed") {
		t.Fatalf("Execute(search) error = %v, want lifecycle error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("search stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "Usage:") || strings.Count(got, "level=ERROR") != 1 {
		t.Fatalf("search stderr = %q, want one error and usage", got)
	}
}

func TestSearchRejectsTypedNilLifecycleService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return (*recordingSearchLifecycle)(nil), nil
	})

	err := entry.Execute([]string{"search", "summary"})
	if err == nil || err.Error() != "create search lifecycle: nil service" {
		t.Fatalf("Execute(search) error = %v, want typed-nil service error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("search stdout = %q, want empty", stdout.String())
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestSearchFactoryRunsAfterVerboseLoggerSetup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	factoryCalled := false
	entry := newSearchEntry(t, &stdout, &stderr, func() (cli.SearchLifecycle, error) {
		factoryCalled = true
		if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("search factory called before verbose logger setup")
		}
		return &recordingSearchLifecycle{}, nil
	})

	if err := entry.Execute([]string{"search", "summary", "--verbose"}); err != nil {
		t.Fatalf("Execute(search --verbose) error = %v", err)
	}
	if !factoryCalled {
		t.Fatal("search factory was not called")
	}
}

func TestSearchRenderFailureIsLoggedOnce(t *testing.T) {
	var stderr bytes.Buffer
	entry := newSearchEntry(t, errWriter{}, &stderr, func() (cli.SearchLifecycle, error) {
		return &recordingSearchLifecycle{result: cli.SearchResult{Powers: []domain.PowerRef{{Type: domain.PowerTypeSkill, Name: "alpha", Version: "v1.0.0"}}}}, nil
	})

	err := entry.Execute([]string{"search", "alpha"})
	if err == nil || !strings.Contains(err.Error(), "write search result") {
		t.Fatalf("Execute(search) error = %v, want render error", err)
	}
	if got := stderr.String(); !strings.Contains(got, "Usage:") || strings.Count(got, "level=ERROR") != 1 {
		t.Fatalf("search stderr = %q, want one error and usage", got)
	}
}

func TestSearchRenderFailurePreservesCompletedPrefix(t *testing.T) {
	stdout := &prefixWriter{writesBeforeError: 1}
	var stderr bytes.Buffer
	entry := newSearchEntry(t, stdout, &stderr, func() (cli.SearchLifecycle, error) {
		return &recordingSearchLifecycle{result: cli.SearchResult{Powers: []domain.PowerRef{
			{Type: domain.PowerTypeMCP, Name: "alpha", Version: "v2.0.0"},
			{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
		}}}, nil
	})

	if err := entry.Execute([]string{"search", "a"}); err == nil {
		t.Fatal("Execute(search) error = nil, want render error")
	}
	if got, want := stdout.String(), "mcp/alpha@v2.0.0\n"; got != want {
		t.Fatalf("search stdout = %q, want completed prefix %q", got, want)
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type prefixWriter struct {
	bytes.Buffer
	writesBeforeError int
}

func (w *prefixWriter) Write(data []byte) (int, error) {
	if w.writesBeforeError == 0 {
		return 0, errors.New("write failed")
	}
	w.writesBeforeError--
	return w.Buffer.Write(data)
}

func (w *prefixWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
