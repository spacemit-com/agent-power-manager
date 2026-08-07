package logging_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"apm/internal/domain"
	"apm/internal/logging"
)

func TestNewLoggerRoutesLevels(t *testing.T) {
	tests := []struct {
		name       string
		verbose    bool
		level      slog.Level
		wantStdout bool
		wantStderr bool
	}{
		{name: "debug disabled", level: slog.LevelDebug},
		{name: "debug enabled", verbose: true, level: slog.LevelDebug, wantStderr: true},
		{name: "custom debug", verbose: true, level: slog.Level(-2), wantStderr: true},
		{name: "info", level: slog.LevelInfo, wantStdout: true},
		{name: "custom info", level: slog.Level(2), wantStdout: true},
		{name: "warn", level: slog.LevelWarn, wantStderr: true},
		{name: "custom warn", level: slog.Level(6), wantStderr: true},
		{name: "error", level: slog.LevelError, wantStderr: true},
		{name: "custom error", level: slog.Level(12), wantStderr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			logger := logging.NewLogger(&stdout, &stderr, tt.verbose)
			logger.LogAttrs(context.Background(), tt.level, "event", slog.String("key", "value"))

			if got := stdout.Len() > 0; got != tt.wantStdout {
				t.Fatalf("stdout = %q, want output %v", stdout.String(), tt.wantStdout)
			}
			if got := stderr.Len() > 0; got != tt.wantStderr {
				t.Fatalf("stderr = %q, want output %v", stderr.String(), tt.wantStderr)
			}
			combined := stdout.String() + stderr.String()
			if combined != "" {
				if strings.Contains(combined, "time=") {
					t.Fatalf("log = %q, want no timestamp", combined)
				}
				if !strings.Contains(combined, "msg=event") || !strings.Contains(combined, "key=value") {
					t.Fatalf("log = %q, want message and attribute", combined)
				}
			}
		})
	}
}

func TestNewLoggerPreservesAttrsAndGroups(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := logging.NewLogger(&stdout, &stderr, true).
		With(slog.String("component", "cli")).
		WithGroup("request")

	logger.Info("parsed", slog.String("command", "search"))
	if got := stdout.String(); !strings.Contains(got, "component=cli") || !strings.Contains(got, "request.command=search") {
		t.Fatalf("stdout = %q, want inherited attr and group", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestSetupLoggingReplacesDefaultLogger(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	logging.SetupLogging(true)
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug disabled after SetupLogging(true)")
	}
	logging.SetupLogging(false)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug enabled after SetupLogging(false)")
	}
}

func TestNewDefaultUsesProcessWriters(t *testing.T) {
	logger := logging.NewDefault(false)
	if logger == nil {
		t.Fatal("NewDefault returned nil")
	}
	if logger.Stdout() != os.Stdout {
		t.Fatalf("Stdout() = %v, want os.Stdout", logger.Stdout())
	}
	if logger.Stderr() != os.Stderr {
		t.Fatalf("Stderr() = %v, want os.Stderr", logger.Stderr())
	}
}

// TestRoutesAndVerbose catches regressions that route normal output or diagnostics
// to the wrong stream, or emit verbose diagnostics while verbose mode is disabled.
func TestRoutesAndVerbose(t *testing.T) {
	var out, errOut bytes.Buffer
	logger := logging.New(&out, &errOut, false)
	if err := logger.Output("result"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Warning("warning"); err != nil {
		t.Fatal(err)
	}
	logger.Verbose("parsed", slog.String("command", "search"))

	if out.String() != "result\n" {
		t.Fatalf("stdout = %q, want result followed by newline", out.String())
	}
	if !strings.Contains(errOut.String(), "warning") {
		t.Fatalf("stderr = %q, want warning", errOut.String())
	}
	if strings.Contains(errOut.String(), "parsed") {
		t.Fatalf("stderr = %q, verbose diagnostic emitted while disabled", errOut.String())
	}

	errOut.Reset()
	logger = logging.New(io.Discard, &errOut, false)
	logger.SetVerbose(true)
	logger.Verbose("parsed", slog.String("command", "search"))
	if !strings.Contains(errOut.String(), "parsed") || !strings.Contains(errOut.String(), "search") {
		t.Fatalf("stderr = %q, want verbose diagnostic and attributes", errOut.String())
	}
}

// TestDiagnosticOutputsAndWriters catches regressions that route diagnostics to
// stdout or stop exposing the writers supplied by callers.
func TestDiagnosticOutputsAndWriters(t *testing.T) {
	var out, errOut bytes.Buffer
	logger := logging.New(&out, &errOut, false)

	if logger.Stdout() != &out || logger.Stderr() != &errOut {
		t.Fatal("writer accessors did not return injected writers")
	}
	if err := logger.Error("failed"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Recovery("recovered"); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want no diagnostic output", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "failed") || !strings.Contains(got, "recovered") {
		t.Fatalf("stderr = %q, want error and recovery messages", got)
	}
}

// TestFieldHelpers catches regressions in the structured field names or values
// consumed by diagnostic logs.
func TestFieldHelpers(t *testing.T) {
	tests := []struct {
		name string
		attr slog.Attr
		key  string
		want string
	}{
		{name: "agent", attr: logging.Agent(domain.AgentID("codex")), key: "agent", want: "codex"},
		{name: "power", attr: logging.Power("search"), key: "power", want: "search"},
		{name: "version", attr: logging.Version("1.0.0"), key: "version", want: "1.0.0"},
		{name: "phase", attr: logging.Phase("install"), key: "phase", want: "install"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.attr.Key != tt.key || tt.attr.Value.String() != tt.want {
				t.Fatalf("attr = (%q, %q), want (%q, %q)", tt.attr.Key, tt.attr.Value.String(), tt.key, tt.want)
			}
		})
	}
}
