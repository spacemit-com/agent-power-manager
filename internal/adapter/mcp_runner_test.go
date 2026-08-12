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

	result, err := newDefaultMCPRunner().Run(
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

func TestMCPRunnerReportsUnavailableBeforeStarting(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	result, err := newDefaultMCPRunner().Run(context.Background(), "missing-agent", nil, "")
	if !errors.Is(err, ErrAgentCLIUnavailable) {
		t.Fatalf("Run() error = %v, want ErrAgentCLIUnavailable", err)
	}
	if result.Started || result.Path != "" {
		t.Fatalf("result = %+v, want no started process", result)
	}
}

func TestMCPRunnerTimesOutAndBoundsOutput(t *testing.T) {
	fakeBin := t.TempDir()
	writeRunnerFake(t, fakeBin, "hermes", `#!/bin/sh
printf '1234567890'
printf 'abcdefghij' >&2
/bin/sleep 5
`)
	t.Setenv("PATH", fakeBin)
	runner := newDefaultMCPRunner()
	runner.timeout = 20 * time.Millisecond
	runner.outputLimit = 5

	startedAt := time.Now()
	result, err := runner.Run(context.Background(), "hermes", nil, "")
	elapsed := time.Since(startedAt)
	if err == nil || !result.Started || !result.TimedOut {
		t.Fatalf("Run() = %+v, %v; want started timeout", result, err)
	}
	if elapsed >= time.Second {
		t.Fatalf("Run() returned after %s, want prompt process-tree timeout", elapsed)
	}
	if result.Stdout != "12345" || result.Stderr != "abcde" {
		t.Fatalf("bounded output = %q/%q, want 12345/abcde", result.Stdout, result.Stderr)
	}
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("truncation flags = %v/%v, want true/true", result.StdoutTruncated, result.StderrTruncated)
	}
}

func TestMCPRunnerHonorsCanceledParentContext(t *testing.T) {
	fakeBin := t.TempDir()
	writeRunnerFake(t, fakeBin, "claude", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", fakeBin)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := newDefaultMCPRunner().Run(ctx, "claude", nil, "")
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

	result, err := newDefaultMCPRunner().Run(context.Background(), "codex", []string{"mcp"}, "")
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
