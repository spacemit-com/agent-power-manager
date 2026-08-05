package lock_test

import (
	"errors"
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
