//go:build !linux && !darwin && !freebsd && !windows

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
	if !info.Mode().IsRegular() {
		t.Fatalf("Agent temporary mode before write = %v, want regular file", info.Mode())
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
}

func assertPublishedTree(t *testing.T, root string, expectations map[string]publishedNodeExpectation) {
	t.Helper()
	for relative, want := range expectations {
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil {
			t.Error(err)
			continue
		}
		if info.IsDir() != want.directory {
			t.Errorf("published %s directory = %t, want %t", relative, info.IsDir(), want.directory)
		}
	}
}
