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
	"apm/internal/logging"
)

type recordingFixLifecycle struct {
	calls int
	err   error
}

func (f *recordingFixLifecycle) Fix(context.Context) error {
	f.calls++
	return f.err
}

func newFixEntry(t *testing.T, stdout, stderr io.Writer, factory cli.FixLifecycleFactory) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		t.Fatal("Fix dispatched to Search factory")
		return nil, nil
	}, func() (cli.ListLifecycle, error) {
		t.Fatal("Fix dispatched to List factory")
		return nil, nil
	}, func() (cli.InstallLifecycle, error) {
		t.Fatal("Fix dispatched to Install factory")
		return nil, nil
	}, func() (cli.UninstallLifecycle, error) {
		t.Fatal("Fix dispatched to Uninstall factory")
		return nil, nil
	}, factory)
}

func TestFixRejectsArgumentsAndCommandFlagsBeforeFactory(t *testing.T) {
	for _, args := range [][]string{
		{"fix", "extra"},
		{"fix", "--agent=codex"},
		{"fix", "--type=skill"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			factoryCalls := 0
			entry := newFixEntry(t, &stdout, &stderr, func() (cli.FixLifecycle, error) {
				factoryCalls++
				return &recordingFixLifecycle{}, nil
			})

			if err := entry.Execute(args); err == nil {
				t.Fatalf("Execute(%q) error = nil, want validation error", args)
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0", factoryCalls)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}

func TestFixFactoryAndServiceFailuresUseRootErrorPath(t *testing.T) {
	factoryErr := errors.New("factory unavailable")
	serviceErr := errors.New("recovery unavailable")
	typedNil := (*recordingFixLifecycle)(nil)
	tests := []struct {
		name    string
		factory cli.FixLifecycleFactory
		want    error
		message string
	}{
		{name: "nil factory", factory: nil, message: "fix lifecycle factory is required"},
		{name: "factory error", factory: func() (cli.FixLifecycle, error) { return nil, factoryErr }, want: factoryErr, message: "create fix lifecycle"},
		{name: "nil service", factory: func() (cli.FixLifecycle, error) { return nil, nil }, message: "create fix lifecycle: nil service"},
		{name: "typed nil service", factory: func() (cli.FixLifecycle, error) { return typedNil, nil }, message: "create fix lifecycle: nil service"},
		{name: "service error", factory: func() (cli.FixLifecycle, error) { return &recordingFixLifecycle{err: serviceErr}, nil }, want: serviceErr, message: "recovery unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newFixEntry(t, &stdout, &stderr, tt.factory)

			err := entry.Execute([]string{"fix"})
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("Execute(fix) error = %v, want %q", err, tt.message)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Execute(fix) error = %v, want wrapped %v", err, tt.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			assertOneErrorAndUsage(t, stderr.String())
		})
	}
}
