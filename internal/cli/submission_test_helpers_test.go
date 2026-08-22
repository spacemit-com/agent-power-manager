package cli_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"apm/internal/cli"
	"apm/internal/logging"
)

type recordingValidateLifecycle struct {
	flags  cli.Flags
	args   []string
	result cli.ValidateResult
	err    error
	calls  int
}

func (l *recordingValidateLifecycle) Validate(_ context.Context, flags cli.Flags, args []string) (cli.ValidateResult, error) {
	l.calls++
	l.flags = flags
	l.args = args
	return l.result, l.err
}

type recordingSubmitLifecycle struct {
	flags  cli.Flags
	args   []string
	result cli.SubmitResult
	err    error
	calls  int
}

func (l *recordingSubmitLifecycle) Submit(_ context.Context, flags cli.Flags, args []string) (cli.SubmitResult, error) {
	l.calls++
	l.flags = flags
	l.args = args
	return l.result, l.err
}

type recordingSubmissionLifecycle struct {
	getIDs    []string
	listFlags []cli.Flags
	result    cli.SubmissionQueryResult
	err       error
	getCalls  int
	listCalls int
}

func (l *recordingSubmissionLifecycle) Get(_ context.Context, id string) (cli.SubmissionQueryResult, error) {
	l.getCalls++
	l.getIDs = append(l.getIDs, id)
	return l.result, l.err
}

func (l *recordingSubmissionLifecycle) List(_ context.Context, flags cli.Flags) (cli.SubmissionQueryResult, error) {
	l.listCalls++
	l.listFlags = append(l.listFlags, flags)
	return l.result, l.err
}

// newSubmissionEntry 构造仅允许 validate/submit/submission 三个工厂被调用的 CLI 入口；
// 其余旧命令工厂被调用即失败，证明新命令不会误分派到旧生命周期。
func unexpectedValidate(t *testing.T) cli.ValidateLifecycleFactory {
	return func() (cli.ValidateLifecycle, error) { t.Fatal("dispatched to Validate factory"); return nil, nil }
}

func unexpectedSubmit(t *testing.T) cli.SubmitLifecycleFactory {
	return func() (cli.SubmitLifecycle, error) { t.Fatal("dispatched to Submit factory"); return nil, nil }
}

func unexpectedSubmission(t *testing.T) cli.SubmissionQueryLifecycleFactory {
	return func() (cli.SubmissionQueryLifecycle, error) {
		t.Fatal("dispatched to Submission factory")
		return nil, nil
	}
}

func newSubmissionEntry(t *testing.T, stdout, stderr io.Writer, validate cli.ValidateLifecycleFactory, submit cli.SubmitLifecycleFactory, submission cli.SubmissionQueryLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		t.Fatal("dispatched to Search factory")
		return nil, nil
	}, func() (cli.ListLifecycle, error) {
		t.Fatal("dispatched to List factory")
		return nil, nil
	}, func() (cli.InstallLifecycle, error) {
		t.Fatal("dispatched to Install factory")
		return nil, nil
	}, func() (cli.UninstallLifecycle, error) {
		t.Fatal("dispatched to Uninstall factory")
		return nil, nil
	}, func() (cli.FixLifecycle, error) {
		t.Fatal("dispatched to Fix factory")
		return nil, nil
	}, validate, submit, submission)
}
