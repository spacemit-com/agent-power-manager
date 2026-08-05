package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/logging"
)

func TestCLIParsesCommandsAndRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantMessage string
	}{
		{name: "search", args: []string{"search", "foo", "--agent=all"}},
		{name: "list", args: []string{"list", "--agent=codex", "--installed"}},
		{name: "install", args: []string{"install", "demo=1.2.3", "--agent=codex", "--type=skill"}},
		{name: "uninstall", args: []string{"uninstall", "demo", "--agent=codex"}},
		{name: "missing agent", args: []string{"search", "foo"}, wantErr: true, wantMessage: "--agent is required"},
		{name: "invalid type", args: []string{"list", "--agent=all", "--type=other"}, wantErr: true, wantMessage: "invalid --type \"other\""},
		{name: "empty version", args: []string{"install", "demo=", "--agent=all"}, wantErr: true, wantMessage: "power version must not be empty"},
		{name: "extra arg", args: []string{"uninstall", "demo", "extra", "--agent=all"}, wantErr: true, wantMessage: "accepts 1 arg(s), received 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := cli.New(logging.New(&stdout, &stderr, false))

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
			if got := stderr.String(); !strings.Contains(got, tt.wantMessage) || !strings.Contains(got, "Usage:") {
				t.Fatalf("invalid command stderr = %q, want error %q and Cobra usage", got, tt.wantMessage)
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
			entry := cli.New(logging.New(&stdout, &stderr, false))

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			entry := cli.New(logging.New(&stdout, &stderr, false))

			if err := entry.Execute(tt.args); err == nil {
				t.Fatalf("Execute(%q) error = nil, want error", tt.args)
			}
			if stdout.Len() != 0 {
				t.Fatalf("error stdout = %q, want empty", stdout.String())
			}
			got := stderr.String()
			if !strings.Contains(got, tt.wantText) || !strings.Contains(got, "Usage:") {
				t.Fatalf("error stderr = %q, want %q and Cobra usage", got, tt.wantText)
			}
		})
	}
}

func TestCLIHelpListsCommandsOnStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := cli.New(logging.New(&stdout, &stderr, false))

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
	entry := cli.New(logging.New(&stdout, &stderr, false))

	if err := entry.Execute([]string{"search", "foo", "--agent=all", "--verbose"}); err != nil {
		t.Fatalf("Execute(verbose search) error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("verbose stdout = %q, want empty", stdout.String())
	}
	got := stderr.String()
	if !strings.Contains(got, "command=search") || !strings.Contains(got, "agent=all") {
		t.Fatalf("verbose stderr = %q, want command and agent attributes", got)
	}
}

func TestCLIRepeatedExecutionDoesNotLeakFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := cli.New(logging.New(&stdout, &stderr, false))

	if err := entry.Execute([]string{"search", "first", "--agent=all", "--verbose"}); err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"search", "second", "--agent=codex"}); err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("second Execute output = stdout %q, stderr %q; want no leaked verbose state", stdout.String(), stderr.String())
	}

	if err := entry.Execute([]string{"search", "third"}); err == nil {
		t.Fatal("third Execute error = nil, want missing --agent after prior executions")
	}
}
