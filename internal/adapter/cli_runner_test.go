package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"apm/internal/utils/filesystem"
)

func TestMCPAdapterResultStatesSupportFailClosedRecovery(t *testing.T) {
	if MCPPresent != "present" || MCPAbsent != "absent" {
		t.Fatalf("presence values = %q/%q", MCPPresent, MCPAbsent)
	}
	states := []MCPMutationState{MCPMutationApplied, MCPMutationNotApplied, MCPMutationUnknown}
	want := []MCPMutationState{"applied", "not-applied", "unknown"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("mutation states = %v, want %v", states, want)
	}
}

func TestDefaultMCPRunnerUsesLookPathAndPreservesArgvAndStdin(t *testing.T) {
	fakeBin := t.TempDir()
	argvPath := filepath.Join(t.TempDir(), "argv")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	writeRunnerFake(t, fakeBin, "codex", `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_ARGV"
/bin/cat > "$FAKE_STDIN"
printf 'ok'
`)
	t.Setenv("PATH", fakeBin)
	t.Setenv("FAKE_ARGV", argvPath)
	t.Setenv("FAKE_STDIN", stdinPath)

	result, err := newDefaultAgentCLIRunner().Run(
		context.Background(),
		"codex",
		[]string{"mcp", "add", "demo", "--", "cmd", "a b", "$HOME", "x;y", "路径🚀"},
		"answer\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(result.Path) != fakeBin {
		t.Fatalf("resolved path = %q, want inside %q", result.Path, fakeBin)
	}
	if result.Stdout != "ok" || result.Stderr != "" || result.ExitCode != 0 || !result.Started || result.TimedOut {
		t.Fatalf("result = %+v", result)
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := "mcp\nadd\ndemo\n--\ncmd\na b\n$HOME\nx;y\n路径🚀\n"
	if string(argv) != wantArgv {
		t.Fatalf("argv = %q, want %q", argv, wantArgv)
	}
	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdin) != "answer\n" {
		t.Fatalf("stdin = %q, want scripted input", stdin)
	}
}

func TestDefaultAgentCLIRunnerResolvesEveryAgentInsideFakeBin(t *testing.T) {
	fakeBin := t.TempDir()
	isolatedHome := t.TempDir()
	for _, executable := range []string{"bianbu-agent", "codex", "claude", "hermes"} {
		writeRunnerFake(t, fakeBin, executable, "#!/bin/sh\nexit 0\n")
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("HOME", isolatedHome)

	for _, executable := range []string{"bianbu-agent", "codex", "claude", "hermes"} {
		result, err := newDefaultAgentCLIRunner().Run(context.Background(), executable, nil, "")
		within, withinErr := filesystem.PathEqualOrWithin(fakeBin, result.Path)
		if err != nil || withinErr != nil || !within || result.Path != filepath.Join(fakeBin, executable) {
			t.Fatalf("Run(%q) path = %q, run=%v containment=%v; want fake-bin", executable, result.Path, err, withinErr)
		}
	}
}

func TestMCPRunnerReportsUnavailableBeforeStarting(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result, err := newDefaultAgentCLIRunner().Run(context.Background(), "missing-agent", nil, "")
	if !errors.Is(err, ErrAgentCLIUnavailable) {
		t.Fatalf("Run() error = %v, want ErrAgentCLIUnavailable", err)
	}
	if result.Started || result.Path != "" {
		t.Fatalf("result = %+v, want no started process", result)
	}
}

func TestMCPRunnerTimesOutPromptly(t *testing.T) {
	fakeBin := t.TempDir()
	writeRunnerFake(t, fakeBin, "hermes", `#!/bin/sh
/bin/sleep 5
`)
	t.Setenv("PATH", fakeBin)
	runner := newDefaultAgentCLIRunner()
	runner.timeout = 20 * time.Millisecond

	startedAt := time.Now()
	result, err := runner.Run(context.Background(), "hermes", nil, "")
	elapsed := time.Since(startedAt)
	if err == nil || !result.Started || !result.TimedOut {
		t.Fatalf("Run() = %+v, %v; want started timeout", result, err)
	}
	if elapsed >= time.Second {
		t.Fatalf("Run() returned after %s, want prompt process-tree timeout", elapsed)
	}
	if result.Stdout != "" || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf("silent timeout result = %+v", result)
	}
}

func TestBoundedWriterLimitsOutputWithoutDependingOnProcessScheduling(t *testing.T) {
	stdout := newBoundedWriter(5)
	stderr := newBoundedWriter(5)
	if count, err := stdout.Write([]byte("1234567890")); err != nil || count != 10 {
		t.Fatalf("stdout Write() = %d, %v", count, err)
	}
	if count, err := stderr.Write([]byte("abcdefghij")); err != nil || count != 10 {
		t.Fatalf("stderr Write() = %d, %v", count, err)
	}
	if stdout.String() != "12345" || stderr.String() != "abcde" {
		t.Fatalf("bounded output = %q/%q, want 12345/abcde", stdout.String(), stderr.String())
	}
	if !stdout.Truncated() || !stderr.Truncated() {
		t.Fatalf("truncation flags = %v/%v, want true/true", stdout.Truncated(), stderr.Truncated())
	}
}

func TestMCPRunnerBoundsSuccessfulCommandOutput(t *testing.T) {
	fakeBin := t.TempDir()
	writeRunnerFake(t, fakeBin, "hermes", `#!/bin/sh
printf '1234567890'
printf 'abcdefghij' >&2
`)
	t.Setenv("PATH", fakeBin)
	runner := newDefaultAgentCLIRunner()
	runner.outputLimit = 5

	result, err := runner.Run(context.Background(), "hermes", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Started || result.TimedOut || result.ExitCode != 0 {
		t.Fatalf("runner result = %+v, want successful started command", result)
	}
	if result.Stdout != "12345" || result.Stderr != "abcde" {
		t.Fatalf("bounded runner output = %q/%q, want 12345/abcde", result.Stdout, result.Stderr)
	}
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("runner truncation flags = %v/%v, want true/true", result.StdoutTruncated, result.StderrTruncated)
	}
}

func TestMCPRunnerHonorsCanceledParentContext(t *testing.T) {
	fakeBin := t.TempDir()
	writeRunnerFake(t, fakeBin, "claude", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", fakeBin)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := newDefaultAgentCLIRunner().Run(ctx, "claude", nil, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if result.Started {
		t.Fatalf("result = %+v, want no started process", result)
	}
}

func TestMCPRunnerCapturesNonzeroExitWithoutDiscardingDiagnostics(t *testing.T) {
	fakeBin := t.TempDir()
	writeRunnerFake(t, fakeBin, "codex", "#!/bin/sh\nprintf 'bad' >&2\nexit 7\n")
	t.Setenv("PATH", fakeBin)

	result, err := newDefaultAgentCLIRunner().Run(context.Background(), "codex", []string{"mcp"}, "")
	if err == nil || !result.Started || result.ExitCode != 7 || !strings.Contains(result.Stderr, "bad") {
		t.Fatalf("Run() = %+v, %v; want exit 7 diagnostics", result, err)
	}
}

func writeRunnerFake(t *testing.T, directory, name, body string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
