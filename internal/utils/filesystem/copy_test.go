package filesystem_test

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/utils/filesystem"
)

func TestCopyDirCopiesTreeContentAndPermissions(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "nested", "destination")
	if err := os.MkdirAll(filepath.Join(source, "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(source, "child"), 0o710); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "root.txt"), []byte("root"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "child", "nested.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := filesystem.CopyDir(source, destination); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		filepath.Join(destination, "root.txt"):            "root",
		filepath.Join(destination, "child", "nested.txt"): "nested",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	for path, want := range map[string]os.FileMode{
		destination:                            0o750,
		filepath.Join(destination, "child"):    0o710,
		filepath.Join(destination, "root.txt"): 0o640,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", path, got, want)
		}
	}
}
