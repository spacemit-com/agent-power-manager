//go:build !windows

package local_test

import (
	"os/exec"
	"syscall"
	"testing"
)

func configureUnprivilegedProcess(t *testing.T, command *exec.Cmd) {
	t.Helper()
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
}
