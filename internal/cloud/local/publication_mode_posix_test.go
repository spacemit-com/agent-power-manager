//go:build linux || darwin || freebsd

package local

import (
	"os"
	"path/filepath"
	"testing"
)

type publishedNodeExpectation struct {
	mode      os.FileMode
	directory bool
}

func assertPrivateAgentStaging(t *testing.T, temporary *os.File) {
	t.Helper()
	info, err := temporary.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("Agent temporary mode before write = %o, want 600", mode)
	}
}

func assertPublishedAgentFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("published Agent mode for %q = %v, want regular file", path, info.Mode())
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Fatalf("published Agent mode for %q = %o, want 644", path, mode)
	}
}

func assertPublishedTree(t *testing.T, root string, expectations map[string]publishedNodeExpectation) {
	t.Helper()
	for relative, want := range expectations {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err != nil {
			t.Error(err)
			continue
		}
		if info.IsDir() != want.directory {
			t.Errorf("published %s directory = %t, want %t", relative, info.IsDir(), want.directory)
		}
		if got := info.Mode().Perm(); got != want.mode {
			t.Errorf("published %s mode = %o, want %o", relative, got, want.mode)
		}
	}
}
