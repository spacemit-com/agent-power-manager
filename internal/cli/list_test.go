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

type recordingListLifecycle struct {
	flags  cli.Flags
	result cli.ListResult
	err    error
	calls  int
}

func (l *recordingListLifecycle) List(_ context.Context, flags cli.Flags) (cli.ListResult, error) {
	l.calls++
	l.flags = flags
	return l.result, l.err
}

func newListEntry(t *testing.T, stdout, stderr io.Writer, factory cli.ListLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		t.Fatal("List dispatched to Search factory")
		return nil, nil
	}, factory)
}

func TestListValidatesConditionalAgentBeforeFactory(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "cloud", args: []string{"list"}},
		{name: "cloud type", args: []string{"list", "--type=skill"}},
		{name: "cloud explicit agent", args: []string{"list", "--agent=all"}, wantErr: "--agent requires --installed"},
		{name: "cloud explicit empty agent", args: []string{"list", "--agent="}, wantErr: "--agent requires --installed"},
		{name: "installed missing agent", args: []string{"list", "--installed"}, wantErr: "--agent is required"},
		{name: "installed whitespace agent", args: []string{"list", "--installed", "--agent= \t "}, wantErr: "--agent is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			lifecycle := &recordingListLifecycle{}
			factoryCalls := 0
			entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
				factoryCalls++
				return lifecycle, nil
			})

			err := entry.Execute(tt.args)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Execute(%q) error = %v", tt.args, err)
				}
				if factoryCalls != 1 || lifecycle.calls != 1 {
					t.Fatalf("factory/List calls = %d/%d, want 1/1", factoryCalls, lifecycle.calls)
				}
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute(%q) error = %v, want %q", tt.args, err, tt.wantErr)
			}
			if factoryCalls != 0 || lifecycle.calls != 0 {
				t.Fatalf("invalid input factory/List calls = %d/%d, want 0/0", factoryCalls, lifecycle.calls)
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid input stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestListPassesOriginalInstalledAgentAndType(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingListLifecycle{}
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"list", "--installed", "--agent= codex ", "--type=plugin"}); err != nil {
		t.Fatalf("Execute(list) error = %v", err)
	}
	want := cli.Flags{Installed: true, Agent: " codex ", Type: "plugin"}
	if lifecycle.flags != want {
		t.Fatalf("List flags = %#v, want %#v", lifecycle.flags, want)
	}
}

func TestListRendersExactCloudAndInstalledLines(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		result cli.ListResult
		want   string
	}{
		{
			name: "cloud",
			args: []string{"list"},
			result: cli.ListResult{Powers: []cli.ListedPower{
				{Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "alpha", Version: "v2.0.0"}},
				{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"}},
			}},
			want: "mcp/alpha@v2.0.0\nskill/beta@v1.0.0\n",
		},
		{
			name: "concrete installed",
			args: []string{"list", "--installed", "--agent=codex"},
			result: cli.ListResult{Powers: []cli.ListedPower{{
				Agent: domain.AgentCodex,
				Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "local", Version: "v1.2.3"},
			}}},
			want: "codex/skill/local@v1.2.3\n",
		},
		{
			name: "all installed",
			args: []string{"list", "--installed", "--agent=all"},
			result: cli.ListResult{Powers: []cli.ListedPower{{
				Agent: domain.AgentClaudeCode,
				Power: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "shared", Version: "v3.0.0"},
			}}},
			want: "claude-code/plugin/shared@v3.0.0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
				return &recordingListLifecycle{result: tt.result}, nil
			})

			if err := entry.Execute(tt.args); err != nil {
				t.Fatalf("Execute(%q) error = %v", tt.args, err)
			}
			if got := stdout.String(); got != tt.want {
				t.Fatalf("stdout = %q, want %q", got, tt.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestListEmptyResultWritesNothing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
		return &recordingListLifecycle{}, nil
	})

	if err := entry.Execute([]string{"list"}); err != nil {
		t.Fatalf("Execute(list) error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
	}
}

func TestListFatalFailureSuppressesResult(t *testing.T) {
	fatalErr := errors.New("list failed")
	lifecycle := &recordingListLifecycle{
		result: cli.ListResult{Powers: []cli.ListedPower{{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "hidden", Version: "v1.0.0"}}}},
		err:    fatalErr,
	}
	var stdout, stderr bytes.Buffer
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) { return lifecycle, nil })

	err := entry.Execute([]string{"list"})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("Execute() error = %v, want %v", err, fatalErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestListRenderFailurePreservesCompletedPrefix(t *testing.T) {
	writeErr := errors.New("writer unavailable")
	stdout := &listPrefixWriter{writesBeforeError: 1, err: writeErr}
	var stderr bytes.Buffer
	entry := newListEntry(t, stdout, &stderr, func() (cli.ListLifecycle, error) {
		return &recordingListLifecycle{result: cli.ListResult{Powers: []cli.ListedPower{
			{Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "alpha", Version: "v2.0.0"}},
			{Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"}},
		}}}, nil
	})

	err := entry.Execute([]string{"list"})
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "write list result") {
		t.Fatalf("Execute() error = %v, want wrapped writer error", err)
	}
	if got, want := stdout.String(), "mcp/alpha@v2.0.0\n"; got != want {
		t.Fatalf("stdout = %q, want completed prefix %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestListPartialFailureRendersSuccessesThenReturnsError(t *testing.T) {
	readErr := errors.New("manifest unreadable")
	lifecycle := &recordingListLifecycle{
		result: cli.ListResult{Powers: []cli.ListedPower{{
			Agent: domain.AgentCodex,
			Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		}}},
		err: &cli.PartialListError{Failures: []cli.AgentFailure{{Agent: domain.AgentClaudeCode, Err: readErr}}},
	}
	var stdout, stderr bytes.Buffer
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) { return lifecycle, nil })

	err := entry.Execute([]string{"list", "--installed", "--agent=all"})
	if !errors.Is(err, readErr) {
		t.Fatalf("Execute() error = %v, want %v", err, readErr)
	}
	if got, want := stdout.String(), "codex/skill/demo@v1.0.0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestListPartialRenderFailureJoinsWriterAndAllAgentCauses(t *testing.T) {
	firstReadErr := errors.New("claude manifest unreadable")
	secondReadErr := errors.New("hermes manifest unreadable")
	writeErr := errors.New("writer unavailable")
	stdout := &listPrefixWriter{writesBeforeError: 1, err: writeErr}
	lifecycle := &recordingListLifecycle{
		result: cli.ListResult{Powers: []cli.ListedPower{
			{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}},
			{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v2.0.0"}},
		}},
		err: &cli.PartialListError{Failures: []cli.AgentFailure{
			{Agent: domain.AgentClaudeCode, Err: firstReadErr},
			{Agent: domain.AgentHermes, Err: secondReadErr},
		}},
	}
	var stderr bytes.Buffer
	entry := newListEntry(t, stdout, &stderr, func() (cli.ListLifecycle, error) { return lifecycle, nil })

	err := entry.Execute([]string{"list", "--installed", "--agent=all"})
	for _, cause := range []error{firstReadErr, secondReadErr, writeErr} {
		if !errors.Is(err, cause) {
			t.Errorf("Execute() error = %v, want cause %v", err, cause)
		}
	}
	var partial *cli.PartialListError
	if !errors.As(err, &partial) {
		t.Fatalf("Execute() error = %v, want PartialListError", err)
	}
	if got, want := stdout.String(), "codex/skill/first@v1.0.0\n"; got != want {
		t.Fatalf("stdout = %q, want completed prefix %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestListFactoryRunsAfterVerboseLoggerSetup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	factoryCalled := false
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
		factoryCalled = true
		if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("List factory called before verbose logger setup")
		}
		return &recordingListLifecycle{}, nil
	})

	if err := entry.Execute([]string{"list", "--verbose"}); err != nil {
		t.Fatalf("Execute(list --verbose) error = %v", err)
	}
	if !factoryCalled {
		t.Fatal("List factory was not called")
	}
}

func TestListFactoryFailuresAreContextual(t *testing.T) {
	constructionErr := errors.New("construction failed")
	tests := []struct {
		name    string
		factory cli.ListLifecycleFactory
		cause   error
		want    string
	}{
		{name: "nil factory", want: "list lifecycle factory is required"},
		{name: "nil service", factory: func() (cli.ListLifecycle, error) { return nil, nil }, want: "create list lifecycle: nil service"},
		{name: "construction error", factory: func() (cli.ListLifecycle, error) { return nil, constructionErr }, cause: constructionErr, want: "create list lifecycle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newListEntry(t, &stdout, &stderr, tt.factory)
			err := entry.Execute([]string{"list"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute() error = %v, want %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("Execute() error = %v, want cause %v", err, tt.cause)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestListRejectsTypedNilLifecycleService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
		return (*recordingListLifecycle)(nil), nil
	})

	err := entry.Execute([]string{"list"})
	if err == nil || err.Error() != "create list lifecycle: nil service" {
		t.Fatalf("Execute(list) error = %v, want typed-nil service error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("list stdout = %q, want empty", stdout.String())
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestListHelpExplainsAgentRequiresInstalled(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
		t.Fatal("help called List factory")
		return nil, nil
	})

	if err := entry.Execute([]string{"list", "--help"}); err != nil {
		t.Fatalf("Execute(list --help) error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "--agent") || !strings.Contains(got, "requires --installed") {
		t.Fatalf("list help = %q, want conditional Agent guidance", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestListRepeatedExecutionDoesNotLeakFlagsLoggerOrService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var services []*recordingListLifecycle
	entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) {
		service := &recordingListLifecycle{}
		services = append(services, service)
		return service, nil
	})

	if err := entry.Execute([]string{"list", "--installed", "--agent=all", "--type=plugin", "--verbose"}); err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") {
		t.Fatalf("first stderr = %q, want debug log", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"list"}); err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if len(services) != 2 || services[0] == services[1] {
		t.Fatalf("services = %#v, want two independent instances", services)
	}
	if got, want := services[0].flags, (cli.Flags{Installed: true, Agent: "all", Type: "plugin"}); got != want {
		t.Fatalf("first flags = %#v, want %#v", got, want)
	}
	if got, want := services[1].flags, (cli.Flags{}); got != want {
		t.Fatalf("second flags = %#v, want %#v", got, want)
	}
	slog.Debug("after non-verbose list")
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("second output = stdout %q, stderr %q; want no leaked verbose state", stdout.String(), stderr.String())
	}
}

func assertOneErrorAndUsage(t *testing.T, stderr string) {
	t.Helper()
	if strings.Count(stderr, "level=ERROR") != 1 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want one Error and Usage", stderr)
	}
}

type listPrefixWriter struct {
	bytes.Buffer
	writesBeforeError int
	err               error
}

func (w *listPrefixWriter) Write(data []byte) (int, error) {
	if w.writesBeforeError == 0 {
		return 0, w.err
	}
	w.writesBeforeError--
	return w.Buffer.Write(data)
}

func (w *listPrefixWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
