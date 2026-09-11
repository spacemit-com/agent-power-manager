package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"apm/internal/logging"
)

// TestWarnGroupWritableDirectoryEmitsOncePerPath 校验同一目录只输出一行，不同目录互不影响；
// 两条目录链对同一目录的检查因此只产生一行告警。
func TestWarnGroupWritableDirectoryEmitsOncePerPath(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	var stderr bytes.Buffer
	slog.SetDefault(logging.NewLogger(&bytes.Buffer{}, &stderr, false))

	// 路径每次唯一，避免与其它用例共享进程级去重状态。
	first := "/home/demo/perm-warning-test/" + t.Name() + "/cache"
	second := "/home/demo/perm-warning-test/" + t.Name() + "/config"

	logging.WarnGroupWritableDirectory(first, 0o775)
	if got := strings.Count(stderr.String(), "\n"); got != 1 {
		t.Fatalf("warn lines after first call = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), "level=WARN") ||
		!strings.Contains(stderr.String(), "path="+first) ||
		!strings.Contains(stderr.String(), "mode=0775") {
		t.Fatalf("warn output = %q, want WARN level with path and mode attributes", stderr.String())
	}

	logging.WarnGroupWritableDirectory(first, 0o775)
	if got := strings.Count(stderr.String(), "\n"); got != 1 {
		t.Fatalf("warn lines after repeated same path = %d, want 1", got)
	}

	logging.WarnGroupWritableDirectory(second, 0o770)
	if got := strings.Count(stderr.String(), "\n"); got != 2 {
		t.Fatalf("warn lines after distinct path = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "mode=0770") {
		t.Fatalf("warn output = %q, want second directory mode", stderr.String())
	}
}
