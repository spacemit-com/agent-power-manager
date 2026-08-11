package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestNewSearchLifecycleSearchesBundledResources(t *testing.T) {
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(originalDirectory, "..", ".."))
	if err := os.Chdir(repositoryRoot); err != nil {
		t.Fatalf("change to repository root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })

	lifecycle, err := newSearchLifecycle()
	if err != nil {
		t.Fatalf("newSearchLifecycle() error = %v", err)
	}
	got, err := lifecycle.Search(context.Background(), cli.Flags{Keyword: "summary"})
	if err != nil {
		t.Fatalf("Search(summary) error = %v", err)
	}
	want := cli.SearchResult{Powers: []domain.PowerRef{{
		Type:    domain.PowerTypeSkill,
		Name:    "foo-before-summary",
		Version: "v0.0.1",
	}}}
	if len(got.Powers) != len(want.Powers) || len(got.Powers) != 1 || got.Powers[0] != want.Powers[0] {
		t.Fatalf("Search(summary) = %#v, want %#v", got, want)
	}
}
