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

type recordingInstallLifecycle struct {
	flags  cli.Flags
	result cli.InstallResult
	err    error
	calls  int
}

func (l *recordingInstallLifecycle) Install(_ context.Context, flags cli.Flags) (cli.InstallResult, error) {
	l.calls++
	l.flags = flags
	return l.result, l.err
}

func newInstallEntry(t *testing.T, stdout, stderr io.Writer, factory cli.InstallLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		t.Fatal("Install dispatched to Search factory")
		return nil, nil
	}, func() (cli.ListLifecycle, error) {
		t.Fatal("Install dispatched to List factory")
		return nil, nil
	}, factory, func() (cli.UninstallLifecycle, error) {
		t.Fatal("Install dispatched to Uninstall factory")
		return nil, nil
	}, func() (cli.FixLifecycle, error) {
		t.Fatal("Install dispatched to Fix factory")
		return nil, nil
	}, func() (cli.ValidateLifecycle, error) {
		t.Fatal("Install dispatched to Validate factory")
		return nil, nil
	}, func() (cli.SubmitLifecycle, error) {
		t.Fatal("Install dispatched to Submit factory")
		return nil, nil
	}, func() (cli.SubmissionQueryLifecycle, error) {
		t.Fatal("Install dispatched to Submission factory")
		return nil, nil
	})
}

func TestInstallPassesOriginalAgentTypeNameAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingInstallLifecycle{}
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"install", "demo=v1.2.3", "--agent= codex ", "--type=plugin"}); err != nil {
		t.Fatalf("Execute(install) error = %v", err)
	}
	want := cli.Flags{Agent: " codex ", Type: "plugin", Name: "demo", Version: "v1.2.3"}
	if lifecycle.calls != 1 || lifecycle.flags != want {
		t.Fatalf("Install calls/flags = %d/%#v, want 1/%#v", lifecycle.calls, lifecycle.flags, want)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
	}
}

func TestInstallPreservesSurroundingNameSpaces(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingInstallLifecycle{}
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"install", "  demo  =v1.2.3", "--agent=all"}); err != nil {
		t.Fatalf("Execute(install) error = %v", err)
	}
	if lifecycle.flags.Name != "  demo  " {
		t.Fatalf("Install name = %q, want surrounding spaces preserved", lifecycle.flags.Name)
	}
}

func TestInstallAcceptsOnlyCanonicalExplicitVersions(t *testing.T) {
	for _, version := range []string{"v0.0.0", "v1.2.3", "v10.20.30"} {
		t.Run(version, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			lifecycle := &recordingInstallLifecycle{}
			entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) { return lifecycle, nil })

			if err := entry.Execute([]string{"install", "demo=" + version, "--agent=all"}); err != nil {
				t.Fatalf("Execute(install %s) error = %v", version, err)
			}
			if lifecycle.calls != 1 || lifecycle.flags.Version != version {
				t.Fatalf("Install calls/version = %d/%q, want 1/%q", lifecycle.calls, lifecycle.flags.Version, version)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
			}
		})
	}
}

func TestInstallRejectsNonCanonicalVersionsBeforeFactory(t *testing.T) {
	for _, version := range []string{
		"1.2.3", "v1.2", "v1.2.3.4", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-alpha", "v1.2.3+build", " v1.2.3", "v1.2.3 ", "v-1.2.3",
	} {
		t.Run(version, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
				t.Fatal("invalid version invoked Install factory")
				return nil, nil
			})

			err := entry.Execute([]string{"install", "demo=" + version, "--agent=all"})
			if err == nil || !strings.Contains(err.Error(), "invalid power version") {
				t.Fatalf("Execute(install %q) error = %v, want canonical version validation", version, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestInstallRendersExactLinesForAllOutcomes(t *testing.T) {
	result := cli.InstallResult{Items: []cli.InstallItem{
		{Agent: domain.AgentClaudeCode, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: cli.InstallOutcomeUpgraded},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: cli.InstallOutcomeDowngraded},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "same", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeUnchanged},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bad", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeFailed},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "later", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeUnexecuted},
	}}
	var stdout, stderr bytes.Buffer
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{result: result}, nil
	})

	if err := entry.Execute([]string{"install", "demo", "--agent=all"}); err != nil {
		t.Fatalf("Execute(install) error = %v", err)
	}
	want := "claude-code/skill/demo@v2.0.0 installed\n" +
		"codex/skill/demo@v2.0.0 upgraded\n" +
		"hermes-agent/skill/demo@v2.0.0 downgraded\n" +
		"codex/skill/same@v1.0.0 unchanged\n" +
		"codex/skill/bad@v1.0.0 failed\n" +
		"hermes-agent/skill/later@v1.0.0 unexecuted\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestInstallFatalFailureSuppressesResult(t *testing.T) {
	fatalErr := errors.New("install failed")
	var stdout, stderr bytes.Buffer
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{
			result: cli.InstallResult{Items: []cli.InstallItem{{
				Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "hidden", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeInstalled,
			}}},
			err: fatalErr,
		}, nil
	})

	err := entry.Execute([]string{"install", "demo", "--agent=all"})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("Execute(install) error = %v, want %v", err, fatalErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	assertOneErrorNoUsage(t, stderr.String())
}

func TestInstallBatchFailureRendersResultThenReturnsError(t *testing.T) {
	cause := errors.New("manifest write failed")
	var stdout, stderr bytes.Buffer
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{
			result: cli.InstallResult{Items: []cli.InstallItem{
				{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeFailed},
				{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeUnexecuted},
			}},
			err: &cli.InstallBatchError{Agent: domain.AgentCodex, Err: cause},
		}, nil
	})

	err := entry.Execute([]string{"install", "demo", "--agent=all"})
	if !errors.Is(err, cause) {
		t.Fatalf("Execute(install) error = %v, want %v", err, cause)
	}
	var batch *cli.InstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Execute(install) error = %v, want InstallBatchError", err)
	}
	if got, want := stdout.String(), "codex/skill/demo@v1.0.0 failed\nhermes-agent/skill/demo@v1.0.0 unexecuted\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	assertOneErrorNoUsage(t, stderr.String())
}

func TestInstallRenderFailurePreservesCompletedPrefix(t *testing.T) {
	writeErr := errors.New("writer unavailable")
	stdout := &installPrefixWriter{writesBeforeError: 1, err: writeErr}
	var stderr bytes.Buffer
	entry := newInstallEntry(t, stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{result: cli.InstallResult{Items: []cli.InstallItem{
			{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeInstalled},
			{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v2.0.0"}, Outcome: cli.InstallOutcomeUpgraded},
		}}}, nil
	})

	err := entry.Execute([]string{"install", "demo", "--agent=all"})
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "write install result") {
		t.Fatalf("Execute(install) error = %v, want wrapped writer error", err)
	}
	if got, want := stdout.String(), "codex/skill/first@v1.0.0 installed\n"; got != want {
		t.Fatalf("stdout = %q, want completed prefix %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestInstallBatchRenderFailureJoinsWriterAndBatchErrors(t *testing.T) {
	batchCause := errors.New("transaction failed")
	writeErr := errors.New("writer unavailable")
	stdout := &installPrefixWriter{writesBeforeError: 1, err: writeErr}
	var stderr bytes.Buffer
	entry := newInstallEntry(t, stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{
			result: cli.InstallResult{Items: []cli.InstallItem{
				{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "first", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeFailed},
				{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "second", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeUnexecuted},
			}},
			err: &cli.InstallBatchError{Agent: domain.AgentCodex, Err: batchCause},
		}, nil
	})

	err := entry.Execute([]string{"install", "demo", "--agent=all"})
	for _, cause := range []error{batchCause, writeErr} {
		if !errors.Is(err, cause) {
			t.Errorf("Execute(install) error = %v, want cause %v", err, cause)
		}
	}
	var batch *cli.InstallBatchError
	if !errors.As(err, &batch) {
		t.Fatalf("Execute(install) error = %v, want InstallBatchError", err)
	}
	if got, want := stdout.String(), "codex/skill/first@v1.0.0 failed\n"; got != want {
		t.Fatalf("stdout = %q, want completed prefix %q", got, want)
	}
	assertOneErrorAndUsage(t, stderr.String())
}

func TestInstallFactoryRunsAfterVerboseLoggerSetup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	factoryCalled := false
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		factoryCalled = true
		if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("Install factory called before verbose logger setup")
		}
		return &recordingInstallLifecycle{}, nil
	})

	if err := entry.Execute([]string{"install", "demo", "--agent=all", "--verbose"}); err != nil {
		t.Fatalf("Execute(install --verbose) error = %v", err)
	}
	if !factoryCalled {
		t.Fatal("Install factory was not called")
	}
}

func TestInstallFactoryFailuresAreContextual(t *testing.T) {
	constructionErr := errors.New("construction failed")
	tests := []struct {
		name    string
		factory cli.InstallLifecycleFactory
		cause   error
		want    string
	}{
		{name: "nil factory", want: "install lifecycle factory is required"},
		{name: "nil service", factory: func() (cli.InstallLifecycle, error) { return nil, nil }, want: "create install lifecycle: nil service"},
		{name: "typed nil service", factory: func() (cli.InstallLifecycle, error) { return (*recordingInstallLifecycle)(nil), nil }, want: "create install lifecycle: nil service"},
		{name: "construction error", factory: func() (cli.InstallLifecycle, error) { return nil, constructionErr }, cause: constructionErr, want: "create install lifecycle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newInstallEntry(t, &stdout, &stderr, tt.factory)

			err := entry.Execute([]string{"install", "demo", "--agent=all"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(install) error = %v, want %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Fatalf("Execute(install) error = %v, want cause %v", err, tt.cause)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestInstallRepeatedExecutionDoesNotLeakFlagsLoggerOrService(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var services []*recordingInstallLifecycle
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		service := &recordingInstallLifecycle{}
		services = append(services, service)
		return service, nil
	})

	if err := entry.Execute([]string{"install", "first=v1.2.3", "--agent=all", "--type=plugin", "--verbose"}); err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") {
		t.Fatalf("first stderr = %q, want debug log", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"install", "second", "--agent=codex"}); err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if len(services) != 2 || services[0] == services[1] {
		t.Fatalf("services = %#v, want two independent instances", services)
	}
	if got, want := services[0].flags, (cli.Flags{Agent: "all", Type: "plugin", Name: "first", Version: "v1.2.3"}); got != want {
		t.Fatalf("first flags = %#v, want %#v", got, want)
	}
	if got, want := services[1].flags, (cli.Flags{Agent: "codex", Name: "second"}); got != want {
		t.Fatalf("second flags = %#v, want %#v", got, want)
	}
	slog.Debug("after non-verbose install")
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("second output = stdout %q, stderr %q; want no leaked verbose state", stdout.String(), stderr.String())
	}
}

func TestInstallDoesNotInvokeUninstallFactory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{}, nil
	})

	if err := entry.Execute([]string{"install", "demo", "--agent=all"}); err != nil {
		t.Fatalf("Execute(install) error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
	}
}

func TestInstallStdoutNewlineCountEqualsItemCount(t *testing.T) {
	result := cli.InstallResult{Items: []cli.InstallItem{
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeInstalled},
		{Agent: domain.AgentClaudeCode, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeUpgraded},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, Outcome: cli.InstallOutcomeUnchanged},
	}}
	var stdout, stderr bytes.Buffer
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &recordingInstallLifecycle{result: result}, nil
	})

	if err := entry.Execute([]string{"install", "demo", "--agent=all"}); err != nil {
		t.Fatalf("Execute(install) error = %v", err)
	}
	if got, want := strings.Count(stdout.String(), "\n"), len(result.Items); got != want {
		t.Fatalf("stdout newlines = %d, want %d (stdout %q)", got, want, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

type warningFatalInstallLifecycle struct {
	warning string
	err     error
}

func (l *warningFatalInstallLifecycle) Install(_ context.Context, _ cli.Flags) (cli.InstallResult, error) {
	slog.Warn(l.warning, slog.String("operation", "install"))
	return cli.InstallResult{}, l.err
}

func TestInstallReleaseFailureSuppressesStdoutAndPreservesWarning(t *testing.T) {
	fatalErr := errors.New("release install lock: injected")
	var stdout, stderr bytes.Buffer
	entry := newInstallEntry(t, &stdout, &stderr, func() (cli.InstallLifecycle, error) {
		return &warningFatalInstallLifecycle{warning: "installed power target is missing", err: fatalErr}, nil
	})

	err := entry.Execute([]string{"install", "demo", "--agent=all"})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("Execute(install) error = %v, want %v", err, fatalErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want suppressed", stdout.String())
	}
	for _, want := range []string{`msg="installed power target is missing"`, "operation=install", "level=WARN"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
	assertOneErrorNoUsage(t, stderr.String())
}

type installPrefixWriter struct {
	bytes.Buffer
	writesBeforeError int
	err               error
}

func (w *installPrefixWriter) Write(data []byte) (int, error) {
	if w.writesBeforeError == 0 {
		return 0, w.err
	}
	w.writesBeforeError--
	return w.Buffer.Write(data)
}

func (w *installPrefixWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}
