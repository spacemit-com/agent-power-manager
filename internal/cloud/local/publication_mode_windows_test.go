//go:build windows

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
	assertWritableRegularFile(t, path)
}

func assertPublishedTree(t *testing.T, root string, expectations map[string]publishedNodeExpectation) {
	t.Helper()
	for relative, want := range expectations {
		path := filepath.Join(root, relative)
		if want.directory {
			assertWritableDirectory(t, path)
			continue
		}
		assertWritableRegularFile(t, path)
	}
}

func assertWritableRegularFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("published path %q mode = %v, want regular file", path, info.Mode())
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open published file %q for writing: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close published file %q: %v", path, err)
	}
}

func assertWritableDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("published path %q mode = %v, want directory", path, info.Mode())
	}
	probe, err := os.CreateTemp(path, ".writable-")
	if err != nil {
		t.Fatalf("create file in published directory %q: %v", path, err)
	}
	probeName := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		t.Fatalf("close directory write probe %q: %v", probeName, closeErr)
	}
	if removeErr := os.Remove(probeName); removeErr != nil {
		t.Fatalf("remove directory write probe %q: %v", probeName, removeErr)
	}
}
