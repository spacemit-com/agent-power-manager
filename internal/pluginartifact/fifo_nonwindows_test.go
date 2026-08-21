//go:build !windows

package pluginartifact_test

import (
	"syscall"
	"testing"
)

func createFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
