package logging_test

import (
	"bytes"
	"context"
	"log/slog"
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
