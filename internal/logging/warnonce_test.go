package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/logging"
)

// TestWarnOnceEmitsOnlyFirstEvent 校验同一 key 只输出一次，且不同 key 互不影响。
func TestWarnOnceEmitsOnlyFirstEvent(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	var stderr bytes.Buffer
	slog.SetDefault(logging.NewLogger(&bytes.Buffer{}, &stderr, false))

	// key 每次唯一，避免与其它用例共享进程级去重状态。
	first := "warn-once-test:" + t.Name() + ":first"
	second := "warn-once-test:" + t.Name() + ":second"
	attributes := []slog.Attr{slog.String("path", "/home/demo/.cache"), slog.String("mode", "0775")}

	logging.WarnOnce(first, "local directory permits group entry mutation", attributes...)
	if got := strings.Count(stderr.String(), "\n"); got != 1 {
		t.Fatalf("warn lines after first call = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), "level=WARN") || !strings.Contains(stderr.String(), "mode=0775") {
		t.Fatalf("warn output = %q, want WARN level with mode attribute", stderr.String())
	}

	logging.WarnOnce(first, "local directory permits group entry mutation", attributes...)
	if got := strings.Count(stderr.String(), "\n"); got != 1 {
		t.Fatalf("warn lines after repeated same key = %d, want 1", got)
	}

	logging.WarnOnce(second, "local directory permits group entry mutation", attributes...)
	if got := strings.Count(stderr.String(), "\n"); got != 2 {
		t.Fatalf("warn lines after distinct key = %d, want 2", got)
	}
}
