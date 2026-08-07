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
		wantLevel  string
		wantWriter string
	}{
		{name: "debug disabled", level: slog.LevelDebug},
		{name: "debug enabled", verbose: true, level: slog.LevelDebug, wantLevel: "DEBUG", wantWriter: "stderr"},
		{name: "custom debug", verbose: true, level: slog.Level(-2), wantLevel: "DEBUG+2", wantWriter: "stderr"},
		{name: "info", level: slog.LevelInfo, wantLevel: "INFO", wantWriter: "stdout"},
		{name: "custom info", level: slog.Level(2), wantLevel: "INFO+2", wantWriter: "stdout"},
		{name: "warn", level: slog.LevelWarn, wantLevel: "WARN", wantWriter: "stderr"},
		{name: "custom warn", level: slog.Level(6), wantLevel: "WARN+2", wantWriter: "stderr"},
		{name: "error", level: slog.LevelError, wantLevel: "ERROR", wantWriter: "stderr"},
		{name: "custom error", level: slog.Level(12), wantLevel: "ERROR+4", wantWriter: "stderr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			logger := logging.NewLogger(&stdout, &stderr, tt.verbose)
			logger.LogAttrs(context.Background(), tt.level, "event", slog.String("key", "value"))

			if tt.wantWriter == "" {
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
				}
				return
			}

			var output string
			switch tt.wantWriter {
			case "stdout":
				output = stdout.String()
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", stderr.String())
				}
			case "stderr":
				output = stderr.String()
				if stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want empty", stdout.String())
				}
			default:
				t.Fatalf("unknown writer %q", tt.wantWriter)
			}

			if strings.Count(output, "\n") != 1 {
				t.Fatalf("log = %q, want exactly one line", output)
			}
			if strings.Contains(output, "time=") {
				t.Fatalf("log = %q, want no timestamp", output)
			}
			for _, want := range []string{"level=" + tt.wantLevel, "msg=event", "key=value"} {
				if !strings.Contains(output, want) {
					t.Fatalf("log = %q, want %q", output, want)
				}
			}
		})
	}
}

func TestNewLoggerPreservesAttrsAndGroups(t *testing.T) {
	tests := []struct {
		name       string
		level      slog.Level
		wantLevel  string
		wantWriter string
	}{
		{name: "debug", level: slog.LevelDebug, wantLevel: "DEBUG", wantWriter: "stderr"},
		{name: "info", level: slog.LevelInfo, wantLevel: "INFO", wantWriter: "stdout"},
		{name: "warn", level: slog.LevelWarn, wantLevel: "WARN", wantWriter: "stderr"},
		{name: "error", level: slog.LevelError, wantLevel: "ERROR", wantWriter: "stderr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			logger := logging.NewLogger(&stdout, &stderr, true).
				With(slog.String("component", "cli")).
				WithGroup("request")

			logger.LogAttrs(context.Background(), tt.level, "parsed", slog.String("command", "search"))

			var output string
			switch tt.wantWriter {
			case "stdout":
				output = stdout.String()
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", stderr.String())
				}
			case "stderr":
				output = stderr.String()
				if stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want empty", stdout.String())
				}
			default:
				t.Fatalf("unknown writer %q", tt.wantWriter)
			}

			if strings.Count(output, "\n") != 1 {
				t.Fatalf("log = %q, want exactly one line", output)
			}
			for _, want := range []string{
				"level=" + tt.wantLevel,
				"msg=parsed",
				"component=cli",
				"request.command=search",
			} {
				if !strings.Contains(output, want) {
					t.Fatalf("log = %q, want %q", output, want)
				}
			}
		})
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
