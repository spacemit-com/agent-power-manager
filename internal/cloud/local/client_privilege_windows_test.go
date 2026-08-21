//go:build windows

package local_test

import (
	"os/exec"
	"testing"
)

func configureUnprivilegedProcess(t *testing.T, _ *exec.Cmd) {
	t.Helper()
	t.Skip("Windows does not provide the Unix credential fixture used by this test")
}
