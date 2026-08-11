package cli_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/logging"
)

func newTestEntry(t *testing.T, stdout, stderr *bytes.Buffer) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	}, func() (cli.SearchLifecycle, error) {
		return emptySearchLifecycle{}, nil
	})
}

type emptySearchLifecycle struct{}

func (emptySearchLifecycle) Search(context.Context, cli.Flags) (cli.SearchResult, error) {
	return cli.SearchResult{}, nil
}

func TestCLIParsesCommandsAndRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantMessage string
	}{
		{name: "search", args: []string{"search", "foo"}},
		{name: "list", args: []string{"list", "--agent=codex", "--installed"}},
		{name: "install", args: []string{"install", "demo=1.2.3", "--agent=codex", "--type=skill"}},
		{name: "uninstall", args: []string{"uninstall", "demo", "--agent=codex"}},
		{name: "plugin type", args: []string{"search", "foo", "--type=plugin"}},
		{name: "mcp type", args: []string{"list", "--agent=codex", "--type=mcp"}},
		{name: "missing agent", args: []string{"list"}, wantErr: true, wantMessage: "--agent is required"},
		{name: "whitespace agent", args: []string{"list", "--agent= \t "}, wantErr: true, wantMessage: "--agent is required"},
		{name: "invalid type", args: []string{"list", "--agent=all", "--type=other"}, wantErr: true, wantMessage: "invalid --type \"other\""},
		{name: "whitespace keyword", args: []string{"search", " \t "}, wantErr: true, wantMessage: "keyword must not be empty"},
		{name: "empty install name", args: []string{"install", "", "--agent=all"}, wantErr: true, wantMessage: "power name must not be empty"},
		{name: "empty version", args: []string{"install", "demo=", "--agent=all"}, wantErr: true, wantMessage: "power version must not be empty"},
		{name: "whitespace uninstall name", args: []string{"uninstall", " \t ", "--agent=all"}, wantErr: true, wantMessage: "power name must not be empty"},
		{name: "extra search arg", args: []string{"search", "foo", "extra"}, wantErr: true, wantMessage: "accepts 1 arg(s), received 2"},
		{name: "extra list arg", args: []string{"list", "extra", "--agent=all"}, wantErr: true, wantMessage: "accepts 0 arg(s), received 1"},
		{name: "extra install arg", args: []string{"install", "demo", "extra", "--agent=all"}, wantErr: true, wantMessage: "accepts 1 arg(s), received 2"},
		{name: "extra uninstall arg", args: []string{"uninstall", "demo", "extra", "--agent=all"}, wantErr: true, wantMessage: "accepts 1 arg(s), received 2"},
		{name: "invalid verbose", args: []string{"search", "foo", "--verbose=invalid"}, wantErr: true, wantMessage: "invalid argument \"invalid\" for \"--verbose\" flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newTestEntry(t, &stdout, &stderr)

			err := entry.Execute(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Execute(%q) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if !tt.wantErr {
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Fatalf("legal command output = stdout %q, stderr %q; want both empty", stdout.String(), stderr.String())
				}
				return
			}
			if stdout.Len() != 0 {
				t.Fatalf("invalid command stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Execute(%q) error = %q, want %q", tt.args, err, tt.wantMessage)
			}
			if got := stderr.String(); !strings.Contains(got, "Usage:") {
				t.Fatalf("invalid command stderr = %q, want Cobra usage", got)
			}
			if count := strings.Count(stderr.String(), "level=ERROR"); count != 1 {
				t.Fatalf("stderr = %q, want exactly one error log, got %d", stderr.String(), count)
			}
		})
	}
}

func TestCLIHelpFormsWriteGeneratedHelpToStdout(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantText string
	}{
		{name: "help command", args: []string{"help"}, wantText: "Available Commands:"},
		{name: "help subcommand", args: []string{"help", "search"}, wantText: "apm search <keyword>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newTestEntry(t, &stdout, &stderr)

			if err := entry.Execute(tt.args); err != nil {
				t.Fatalf("Execute(%q) error = %v", tt.args, err)
			}
			if !strings.Contains(stdout.String(), tt.wantText) {
				t.Fatalf("help stdout = %q, want %q", stdout.String(), tt.wantText)
			}
			if stderr.Len() != 0 {
				t.Fatalf("help stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestCLIExecutionErrorsWriteErrorAndGeneratedUsageToStderr(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantText string
	}{
		{name: "unknown command", args: []string{"unknown"}, wantText: "unknown command \"unknown\""},
		{name: "invalid flag before help", args: []string{"--bad", "--help"}, wantText: "unknown flag: --bad"},
		{name: "invalid verbose", args: []string{"search", "foo", "--verbose=invalid"}, wantText: "invalid argument \"invalid\" for \"--verbose\" flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := newTestEntry(t, &stdout, &stderr)

			err := entry.Execute(tt.args)
			if err == nil {
				t.Fatalf("Execute(%q) error = nil, want error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("Execute(%q) error = %q, want %q", tt.args, err, tt.wantText)
			}
			if stdout.Len() != 0 {
				t.Fatalf("error stdout = %q, want empty", stdout.String())
			}
			got := stderr.String()
			if !strings.Contains(got, "Usage:") {
				t.Fatalf("error stderr = %q, want Cobra usage", got)
			}
			if count := strings.Count(stderr.String(), "level=ERROR"); count != 1 {
				t.Fatalf("stderr = %q, want exactly one error log, got %d", stderr.String(), count)
			}
		})
	}
}

func TestCLIHelpListsCommandsOnStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newTestEntry(t, &stdout, &stderr)

	if err := entry.Execute([]string{"--help"}); err != nil {
		t.Fatalf("Execute(--help) error = %v", err)
	}
	for _, command := range []string{"search", "list", "install", "uninstall"} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("help stdout = %q, want command %q", stdout.String(), command)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("help stderr = %q, want empty", stderr.String())
	}
}

func TestCLIVerboseWritesOnlyDiagnosticsToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newTestEntry(t, &stdout, &stderr)

	if err := entry.Execute([]string{"search", "foo", "--type=plugin", "--verbose"}); err != nil {
		t.Fatalf("Execute(verbose search) error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("verbose stdout = %q, want empty", stdout.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "level=DEBUG") {
		t.Fatalf("verbose stderr = %q, want debug level", got)
	}
	if strings.Contains(got, "time=") {
		t.Fatalf("verbose stderr = %q, want no timestamp", got)
	}
	for _, attribute := range []string{"command=search", "type=plugin", "args=[foo]"} {
		if !strings.Contains(got, attribute) {
			t.Fatalf("verbose stderr = %q, want attribute %q", got, attribute)
		}
	}
	if strings.Contains(got, "agent=") {
		t.Fatalf("verbose stderr = %q, want no agent attribute", got)
	}
}

func TestCLIRepeatedExecutionDoesNotLeakFlagsOrLogging(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newTestEntry(t, &stdout, &stderr)

	if err := entry.Execute([]string{"search", "first", "--type=plugin", "--verbose"}); err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") {
		t.Fatalf("first stderr = %q, want debug log", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"search", "second", "--verbose"}); err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "level=DEBUG") || strings.Contains(got, "type=plugin") {
		t.Fatalf("second stderr = %q, want fresh verbose logging and no leaked type", got)
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"search", "third"}); err != nil {
		t.Fatalf("third Execute error = %v", err)
	}
	slog.Debug("after non-verbose execution", slog.String("command", "search"))
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("third Execute output = stdout %q, stderr %q; want no leaked verbose state", stdout.String(), stderr.String())
	}
}
