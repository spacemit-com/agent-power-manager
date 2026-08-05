package paths_test

import (
	"path/filepath"
	"testing"

	"apm/internal/paths"
)

func TestLockPathUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := paths.LockPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".apm", "apm.lock"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
