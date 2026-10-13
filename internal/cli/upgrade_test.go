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

type recordingUpgradeLifecycle struct {
	flags  cli.Flags
	result cli.UpgradeResult
	err    error
	calls  int
}

func (l *recordingUpgradeLifecycle) Upgrade(_ context.Context, flags cli.Flags) (cli.UpgradeResult, error) {
	l.calls++
	l.flags = flags
	return l.result, l.err
}

// newUpgradeEntry 构造只注册 Upgrade 的 CLI 入口；任何其它 factory 被调用即失败。
func newUpgradeEntry(t *testing.T, stdout, stderr io.Writer, factory cli.UpgradeLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	unexpected := func(name string) {
		t.Fatalf("Upgrade dispatched to %s factory", name)
	}
	return cli.NewWithUpgrade(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		unexpected("Search")
		return nil, nil
	}, func() (cli.ListLifecycle, error) {
		unexpected("List")
		return nil, nil
	}, func() (cli.InstallLifecycle, error) {
		unexpected("Install")
		return nil, nil
	}, func() (cli.UninstallLifecycle, error) {
		unexpected("Uninstall")
		return nil, nil
	}, func() (cli.FixLifecycle, error) {
		unexpected("Fix")
		return nil, nil
	}, func() (cli.ValidateLifecycle, error) {
		unexpected("Validate")
		return nil, nil
	}, func() (cli.SubmitLifecycle, error) {
		unexpected("Submit")
		return nil, nil
	}, func() (cli.SubmissionQueryLifecycle, error) {
		unexpected("Submission")
		return nil, nil
	}, factory)
}

func newRecordingUpgradeEntry(t *testing.T, stdout, stderr io.Writer, lifecycle *recordingUpgradeLifecycle) *cli.CLIEntry {
	t.Helper()
	return newUpgradeEntry(t, stdout, stderr, func() (cli.UpgradeLifecycle, error) { return lifecycle, nil })
}

func TestUpgradeDefaultsAgentToAllAndForwardsType(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingUpgradeLifecycle{}
	entry := newRecordingUpgradeEntry(t, &stdout, &stderr, lifecycle)

	if err := entry.Execute([]string{"upgrade", "--type=plugin"}); err != nil {
		t.Fatalf("Execute(upgrade) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("Upgrade calls = %d, want 1", lifecycle.calls)
	}
	if lifecycle.flags.Agent != string(domain.AllAgents) {
		t.Fatalf("Upgrade agent = %q, want %q", lifecycle.flags.Agent, domain.AllAgents)
	}
	if lifecycle.flags.Type != "plugin" {
		t.Fatalf("Upgrade type = %q, want plugin", lifecycle.flags.Type)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
	}
}

func TestUpgradeForwardsConcreteFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingUpgradeLifecycle{}
	entry := newRecordingUpgradeEntry(t, &stdout, &stderr, lifecycle)

	if err := entry.Execute([]string{"upgrade", "--agent=codex", "--type=skill", "--dry-run", "--json"}); err != nil {
		t.Fatalf("Execute(upgrade) error = %v", err)
	}
	want := cli.Flags{Agent: "codex", Type: "skill", DryRun: true, JSON: true}
	if lifecycle.flags != want {
		t.Fatalf("Upgrade flags = %#v, want %#v", lifecycle.flags, want)
	}
}

func TestUpgradeNormalizesLegacyAgentTypeAlias(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingUpgradeLifecycle{}
	entry := newRecordingUpgradeEntry(t, &stdout, &stderr, lifecycle)

	if err := entry.Execute([]string{"upgrade", "--type=agent", "--agent=bibit"}); err != nil {
		t.Fatalf("Execute(upgrade --type=agent) error = %v", err)
	}
	if lifecycle.flags.Type != "expert" {
		t.Fatalf("Upgrade type = %q, want expert", lifecycle.flags.Type)
	}
}

func TestUpgradeRejectsPositionalArgumentsBeforeFactory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newUpgradeEntry(t, &stdout, &stderr, func() (cli.UpgradeLifecycle, error) {
		t.Fatal("positional argument invoked Upgrade factory")
		return nil, nil
	})

	err := entry.Execute([]string{"upgrade", "demo"})
	if err == nil || !strings.Contains(err.Error(), "accepts 0 arg(s), received 1") {
		t.Fatalf("Execute(upgrade demo) error = %v, want positional rejection", err)
	}
}

func TestUpgradeRejectsInvalidFlagsBeforeFactory(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid agent", args: []string{"upgrade", "--agent=foo"}, want: `invalid --agent "foo"`},
		{name: "invalid type", args: []string{"upgrade", "--type=other"}, want: `invalid --type "other"`},
		{name: "expert requires bibit", args: []string{"upgrade", "--type=expert", "--agent=codex"}, want: "--type expert only supports --agent bibit"},
		{name: "expert rejects all", args: []string{"upgrade", "--type=expert", "--agent=all"}, want: "--type expert only supports --agent bibit"},
		{name: "unknown flag", args: []string{"upgrade", "--name=demo"}, want: "unknown flag: --name"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newUpgradeEntry(t, &stdout, &stderr, func() (cli.UpgradeLifecycle, error) {
				t.Fatal("invalid flags invoked Upgrade factory")
				return nil, nil
			})

			err := entry.Execute(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Execute(%q) error = %v, want %q", test.args, err, test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid command stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestUpgradeRequiresLifecycleFactory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newUpgradeEntry(t, &stdout, &stderr, nil)

	err := entry.Execute([]string{"upgrade"})
	if err == nil || !strings.Contains(err.Error(), "upgrade lifecycle factory is required") {
		t.Fatalf("Execute(upgrade) error = %v, want missing factory error", err)
	}
}

func TestUpgradeRejectsTypedNilLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var typedNil *recordingUpgradeLifecycle
	entry := newUpgradeEntry(t, &stdout, &stderr, func() (cli.UpgradeLifecycle, error) { return typedNil, nil })

	err := entry.Execute([]string{"upgrade"})
	if err == nil || !strings.Contains(err.Error(), "nil service") {
		t.Fatalf("Execute(upgrade) error = %v, want nil service error", err)
	}
}

func TestUpgradeRendersBatchFailureThenReturnsNonZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cause := errors.New("plugin cache missing")
	ref := domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v0.1.0"}
	lifecycle := &recordingUpgradeLifecycle{
		result: cli.UpgradeResult{Items: []cli.UpgradeItem{
			{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: cli.UpgradeOutcomeFailed},
			{Agent: domain.AgentCodex, Current: ref, Target: ref, Outcome: cli.UpgradeOutcomeUnexecuted},
		}},
		err: &cli.UpgradeBatchError{Failures: []cli.UpgradeFailure{
			{Agent: domain.AgentCodex, Power: ref, Err: cause},
		}},
	}
	entry := newRecordingUpgradeEntry(t, &stdout, &stderr, lifecycle)

	err := entry.Execute([]string{"upgrade"})
	if err == nil {
		t.Fatal("Execute(upgrade) error = nil, want batch failure")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("Execute(upgrade) error = %v, want cause %v", err, cause)
	}
	want := "codex/plugin/demo-plugin@v0.1.0 failed\ncodex/plugin/demo-plugin@v0.1.0 unexecuted\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	// 批处理失败属于 runtime failure：不得打印 usage。
	if strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want no usage for batch failure", stderr.String())
	}
}

func TestUpgradeSuppressesBusinessLinesOnFatalError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingUpgradeLifecycle{err: errors.New("acquire upgrade lock: APM lock is already held")}
	entry := newRecordingUpgradeEntry(t, &stdout, &stderr, lifecycle)

	err := entry.Execute([]string{"upgrade"})
	if err == nil || !strings.Contains(err.Error(), "APM lock is already held") {
		t.Fatalf("Execute(upgrade) error = %v, want lock error", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("fatal stdout = %q, want empty", stdout.String())
	}
	if strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr = %q, want no usage for runtime failure", stderr.String())
	}
}
