package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"apm/internal/cli"
)

func TestFixInvokesLifecycleOnceWithoutOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingFixLifecycle{}
	entry := newFixEntry(t, &stdout, &stderr, func() (cli.FixLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"fix"}); err != nil {
		t.Fatalf("Execute(fix) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("Fix calls = %d, want 1", lifecycle.calls)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = stdout %q, stderr %q; want empty", stdout.String(), stderr.String())
	}
}

func TestFixVerboseInitializesLogging(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newFixEntry(t, &stdout, &stderr, func() (cli.FixLifecycle, error) {
		return &recordingFixLifecycle{}, nil
	})

	if err := entry.Execute([]string{"--verbose", "fix"}); err != nil {
		t.Fatalf("Execute(verbose fix) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") || !strings.Contains(stderr.String(), "command=fix") {
		t.Fatalf("stderr = %q, want Fix debug diagnostics", stderr.String())
	}
}
