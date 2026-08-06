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
		destination:                                       0o750,
		filepath.Join(destination, "child"):               0o710,
		filepath.Join(destination, "root.txt"):            0o640,
		filepath.Join(destination, "child", "nested.txt"): 0o600,
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

func TestCopyDirCopiesReadOnlySourceDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(source, 0o700); err != nil {
			t.Error(err)
		}
	})

	if err := filesystem.CopyDir(source, destination); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(destination, 0o700); err != nil {
			t.Error(err)
		}
	})

	content, err := os.ReadFile(filepath.Join(destination, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), "content"; got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o500); got != want {
		t.Fatalf("destination mode = %o, want %o", got, want)
	}
}
