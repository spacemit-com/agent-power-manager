package lock_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/lock"
)

func TestFlockContendsAndCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "apm.lock")
	first, second := lock.New(path), lock.New(path)

	unlock, err := first.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	parentInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := parentInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("lock parent mode = %o, want 700", got)
	}
	if _, err = second.Acquire(); !errors.Is(err, lock.ErrLocked) {
		t.Fatalf("err=%v", err)
	}
	if err = unlock.Release(); err != nil {
		t.Fatal(err)
	}

	again, err := second.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err = again.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLockUsesAPMHomeWithoutTouchingHome(t *testing.T) {
	realHome := t.TempDir()
	isolationHome := filepath.Join(t.TempDir(), "sandbox")
	t.Setenv("HOME", realHome)
	t.Setenv("APM_HOME", isolationHome)

	locker, err := lock.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := locker.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err = unlock.Release(); err != nil {
		t.Fatal(err)
	}

	if _, err = os.Stat(filepath.Join(isolationHome, ".apm", "apm.lock")); err != nil {
		t.Fatalf("stat isolated lock: %v", err)
	}
	if _, err = os.Stat(filepath.Join(realHome, ".apm")); !os.IsNotExist(err) {
		t.Fatalf("real HOME was touched: %v", err)
	}
}
