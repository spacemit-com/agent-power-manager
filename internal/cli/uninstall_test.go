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

type recordingUninstallLifecycle struct {
	flags  cli.Flags
	result cli.UninstallResult
	err    error
	calls  int
}

func (l *recordingUninstallLifecycle) Uninstall(_ context.Context, flags cli.Flags) (cli.UninstallResult, error) {
	l.calls++
	l.flags = flags
	return l.result, l.err
}

func newUninstallEntry(t *testing.T, stdout, stderr io.Writer, factory cli.UninstallLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		t.Fatal("Uninstall dispatched to Search factory")
		return nil, nil
	}, func() (cli.ListLifecycle, error) {
		t.Fatal("Uninstall dispatched to List factory")
		return nil, nil
	}, func() (cli.InstallLifecycle, error) {
		t.Fatal("Uninstall dispatched to Install factory")
		return nil, nil
	}, factory, func() (cli.FixLifecycle, error) {
		t.Fatal("Uninstall dispatched to Fix factory")
		return nil, nil
	}, func() (cli.ValidateLifecycle, error) {
		t.Fatal("Uninstall dispatched to Validate factory")
		return nil, nil
	}, func() (cli.SubmitLifecycle, error) {
		t.Fatal("Uninstall dispatched to Submit factory")
		return nil, nil
	}, func() (cli.SubmissionQueryLifecycle, error) {
		t.Fatal("Uninstall dispatched to Submission factory")
		return nil, nil
	})
}

func TestUninstallPassesOriginalAgentTypeAndName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingUninstallLifecycle{}
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"uninstall", "  Demo  ", "--agent= codex ", "--type=plugin"}); err != nil {
		t.Fatalf("Execute(uninstall) error = %v", err)
	}
	want := cli.Flags{Agent: " codex ", Type: "plugin", Name: "  Demo  "}
	if lifecycle.calls != 1 || lifecycle.flags != want {
		t.Fatalf("Uninstall calls/flags = %d/%#v, want 1/%#v", lifecycle.calls, lifecycle.flags, want)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q, stderr %q; want both empty", stdout.String(), stderr.String())
	}
}

func TestUninstallRejectsVersionSyntaxBeforeFactory(t *testing.T) {
	for _, argument := range []string{"demo=v1.2.3", "demo=", "a=b=c"} {
		t.Run(argument, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
				t.Fatal("version syntax invoked Uninstall factory")
				return nil, nil
			})

			err := entry.Execute([]string{"uninstall", argument, "--agent=all"})
			if err == nil || !strings.Contains(err.Error(), "uninstall does not accept a version") {
				t.Fatalf("Execute(uninstall %q) error = %v, want version rejection", argument, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestUninstallRejectsInvalidInputBeforeFactory(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "whitespace name", args: []string{"uninstall", " \t ", "--agent=all"}, want: "power name must not be empty"},
		{name: "missing agent", args: []string{"uninstall", "demo"}, want: "--agent is required"},
		{name: "whitespace agent", args: []string{"uninstall", "demo", "--agent= \t "}, want: "--agent is required"},
		{name: "invalid type", args: []string{"uninstall", "demo", "--agent=all", "--type=other"}, want: "invalid --type \"other\""},
		{name: "extra argument", args: []string{"uninstall", "demo", "extra", "--agent=all"}, want: "accepts 1 arg(s), received 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
				t.Fatal("invalid input invoked Uninstall factory")
				return nil, nil
			})

			err := entry.Execute(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(%q) error = %v, want %q", tt.args, err, tt.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestUninstallRejectsUnknownFlagBeforeFactory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		t.Fatal("unknown flag invoked Uninstall factory")
		return nil, nil
	})

	err := entry.Execute([]string{"uninstall", "demo", "--agent=all", "--unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --unknown") {
		t.Fatalf("Execute(uninstall --unknown) error = %v, want unknown flag", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestUninstallRendersCompletedAndAgentOnlyUnexecutedLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &recordingUninstallLifecycle{result: cli.UninstallResult{Items: []cli.UninstallItem{
			{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo", Version: "v1.2.3"}, Outcome: cli.UninstallOutcomeCompleted},
			{Agent: domain.AgentHermes, Outcome: cli.UninstallOutcomeUnexecuted},
		}}}, nil
	})

	if err := entry.Execute([]string{"uninstall", "demo", "--agent=all"}); err != nil {
		t.Fatalf("Execute(uninstall) error = %v", err)
	}
	if got, want := stdout.String(), "codex/plugin/demo@v1.2.3 completed\nhermes-agent unexecuted\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUninstallFatalFailureSuppressesResult(t *testing.T) {
	fatalErr := errors.New("uninstall failed")
	var stdout, stderr bytes.Buffer
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &recordingUninstallLifecycle{
			result: cli.UninstallResult{Items: []cli.UninstallItem{{
				Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "hidden", Version: "v1.0.0"}, Outcome: cli.UninstallOutcomeCompleted,
			}}},
			err: fatalErr,
		}, nil
	})

	err := entry.Execute([]string{"uninstall", "demo", "--agent=all"})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("Execute(uninstall) error = %v, want %v", err, fatalErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	assertOneErrorNoUsage(t, stderr.String())
}

func TestUninstallBatchFailureRendersResultThenReturnsCause(t *testing.T) {
	cause := errors.New("manifest write failed")
	var stdout, stderr bytes.Buffer
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &recordingUninstallLifecycle{
			result: cli.UninstallResult{Items: []cli.UninstallItem{
				{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.UninstallOutcomeFailed},
				{Agent: domain.AgentHermes, Outcome: cli.UninstallOutcomeUnexecuted},
			}},
			err: &cli.UninstallBatchError{Agent: domain.AgentCodex, Err: cause},
		}, nil
	})

	err := entry.Execute([]string{"uninstall", "demo", "--agent=all"})
	if !errors.Is(err, cause) {
		t.Fatalf("Execute(uninstall) error = %v, want %v", err, cause)
	}
	var batch *cli.UninstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Execute(uninstall) error = %v, want UninstallBatchError", err)
	}
	if got, want := stdout.String(), "codex/skill/demo@v1.0.0 failed\nhermes-agent unexecuted\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	assertOneErrorNoUsage(t, stderr.String())
}

func TestUninstallRenderFailurePreservesCompletedPrefix(t *testing.T) {
	writeErr := errors.New("writer unavailable")
	stdout := &uninstallPrefixWriter{writesBeforeError: 1, err: writeErr}
	var stderr bytes.Buffer
	entry := newUninstallEntry(t, stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &recordingUninstallLifecycle{result: cli.UninstallResult{Items: []cli.UninstallItem{
			{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}, Outcome: cli.UninstallOutcomeCompleted},
			{Agent: domain.AgentHermes, Outcome: cli.UninstallOutcomeUnexecuted},
		}}}, nil
	})

	err := entry.Execute([]string{"uninstall", "demo", "--agent=all"})
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "write uninstall result") {
		t.Fatalf("Execute(uninstall) error = %v, want wrapped writer error", err)
	}
	if got, want := stdout.String(), "codex/skill/first@v1.0.0 completed\n"; got != want {
		t.Fatalf("stdout = %q, want completed prefix %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestUninstallBatchRenderFailureJoinsWriterAndBatchCauses(t *testing.T) {
	batchCause := errors.New("transaction failed")
	writeErr := errors.New("writer unavailable")
	stdout := &uninstallPrefixWriter{writesBeforeError: 1, err: writeErr}
	var stderr bytes.Buffer
	entry := newUninstallEntry(t, stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &recordingUninstallLifecycle{
			result: cli.UninstallResult{Items: []cli.UninstallItem{
				{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}, Outcome: cli.UninstallOutcomeFailed},
				{Agent: domain.AgentHermes, Outcome: cli.UninstallOutcomeUnexecuted},
			}},
			err: &cli.UninstallBatchError{Agent: domain.AgentCodex, Err: batchCause},
		}, nil
	})

	err := entry.Execute([]string{"uninstall", "demo", "--agent=all"})
	for _, cause := range []error{batchCause, writeErr} {
		if !errors.Is(err, cause) {
			t.Errorf("Execute(uninstall) error = %v, want cause %v", err, cause)
		}
	}
	var batch *cli.UninstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Execute(uninstall) error = %v, want UninstallBatchError", err)
	}
	if got, want := stdout.String(), "codex/skill/first@v1.0.0 failed\n"; got != want {
		t.Fatalf("stdout = %q, want completed prefix %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestUninstallFactoryRunsAfterVerboseLoggerSetup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	factoryCalled := false
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		factoryCalled = true
		if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("Uninstall factory called before verbose logger setup")
		}
		return &recordingUninstallLifecycle{}, nil
	})

	if err := entry.Execute([]string{"uninstall", "demo", "--agent=all", "--verbose"}); err != nil {
		t.Fatalf("Execute(uninstall --verbose) error = %v", err)
	}
	if !factoryCalled {
		t.Fatal("Uninstall factory was not called")
	}
}

func TestUninstallFactoryFailuresAreContextual(t *testing.T) {
	constructionErr := errors.New("construction failed")
	tests := []struct {
		name    string
		factory cli.UninstallLifecycleFactory
		cause   error
		want    string
	}{
		{name: "nil factory", want: "uninstall lifecycle factory is required"},
		{name: "nil service", factory: func() (cli.UninstallLifecycle, error) { return nil, nil }, want: "create uninstall lifecycle: nil service"},
		{name: "typed nil service", factory: func() (cli.UninstallLifecycle, error) { return (*recordingUninstallLifecycle)(nil), nil }, want: "create uninstall lifecycle: nil service"},
		{name: "construction error", factory: func() (cli.UninstallLifecycle, error) { return nil, constructionErr }, cause: constructionErr, want: "create uninstall lifecycle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newUninstallEntry(t, &stdout, &stderr, tt.factory)

			err := entry.Execute([]string{"uninstall", "demo", "--agent=all"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(uninstall) error = %v, want %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("Execute(uninstall) error = %v, want cause %v", err, tt.cause)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestUninstallRepeatedExecutionDoesNotLeakFlagsLoggerOrService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var services []*recordingUninstallLifecycle
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		service := &recordingUninstallLifecycle{}
		services = append(services, service)
		return service, nil
	})

	if err := entry.Execute([]string{"uninstall", "first", "--agent=all", "--type=plugin", "--verbose"}); err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") {
		t.Fatalf("first stderr = %q, want debug log", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"uninstall", "second", "--agent=codex"}); err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if len(services) != 2 || services[0] == services[1] {
		t.Fatalf("services = %#v, want two independent instances", services)
	}
	if got, want := services[0].flags, (cli.Flags{Agent: "all", Type: "plugin", Name: "first"}); got != want {
		t.Fatalf("first flags = %#v, want %#v", got, want)
	}
	if got, want := services[1].flags, (cli.Flags{Agent: "codex", Name: "second"}); got != want {
		t.Fatalf("second flags = %#v, want %#v", got, want)
	}
	slog.Debug("after non-verbose uninstall")
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("second output = stdout %q, stderr %q; want no leaked verbose state", stdout.String(), stderr.String())
	}
}

func TestUninstallStdoutNewlineCountEqualsItemCount(t *testing.T) {
	result := cli.UninstallResult{Items: []cli.UninstallItem{
		{Agent: domain.AgentCodex, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.UninstallOutcomeCompleted},
		{Agent: domain.AgentClaudeCode, Power: &domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.UninstallOutcomeCompleted},
		{Agent: domain.AgentHermes, Outcome: cli.UninstallOutcomeUnexecuted},
	}}
	var stdout, stderr bytes.Buffer
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &recordingUninstallLifecycle{result: result}, nil
	})

	if err := entry.Execute([]string{"uninstall", "demo", "--agent=all"}); err != nil {
		t.Fatalf("Execute(uninstall) error = %v", err)
	}
	if got, want := strings.Count(stdout.String(), "\n"), len(result.Items); got != want {
		t.Fatalf("stdout newlines = %d, want %d (stdout %q)", got, want, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

type warningFatalUninstallLifecycle struct {
	warning string
	err     error
}

func (l *warningFatalUninstallLifecycle) Uninstall(_ context.Context, _ cli.Flags) (cli.UninstallResult, error) {
	slog.Warn(l.warning, slog.String("operation", "uninstall"))
	return cli.UninstallResult{}, l.err
}

func TestUninstallReleaseFailureSuppressesStdoutAndPreservesWarning(t *testing.T) {
	fatalErr := errors.New("release uninstall lock: injected")
	var stdout, stderr bytes.Buffer
	entry := newUninstallEntry(t, &stdout, &stderr, func() (cli.UninstallLifecycle, error) {
		return &warningFatalUninstallLifecycle{warning: "installed power target is missing", err: fatalErr}, nil
	})

	err := entry.Execute([]string{"uninstall", "demo", "--agent=all"})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("Execute(uninstall) error = %v, want %v", err, fatalErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want suppressed", stdout.String())
	}
	for _, want := range []string{`msg="installed power target is missing"`, "operation=uninstall", "level=WARN"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
	assertOneErrorNoUsage(t, stderr.String())
}

type uninstallPrefixWriter struct {
	bytes.Buffer
	writesBeforeError int
	err               error
}

func (w *uninstallPrefixWriter) Write(data []byte) (int, error) {
	if w.writesBeforeError == 0 {
		return 0, w.err
	}
	w.writesBeforeError--
	return w.Buffer.Write(data)
}

func (w *uninstallPrefixWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
