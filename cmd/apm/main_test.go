package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cli"
	"apm/internal/config"
	"apm/internal/domain"
)

func TestNewSearchLifecycleSearchesBundledResources(t *testing.T) {
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	changeToRepositoryRoot(t)

	service, err := newSearchLifecycle()
	if err != nil {
		t.Fatalf("newSearchLifecycle() error = %v", err)
	}
	got, err := service.Search(context.Background(), cli.Flags{Keyword: "summary"})
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

func TestNewListLifecycleListsBundledCloud(t *testing.T) {
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	changeToRepositoryRoot(t)

	service, err := newListLifecycle()
	if err != nil {
		t.Fatalf("newListLifecycle() error = %v", err)
	}
	result, err := service.List(context.Background(), cli.Flags{Type: "skill"})
	if err != nil {
		t.Fatalf("List(skill) error = %v", err)
	}
	want := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.0.1"}
	found := false
	for _, item := range result.Powers {
		if item.Agent == "" && item.Power == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("List(skill) = %#v, want bundled %#v", result, want)
	}
}

func TestNewListLifecycleListsInstalledManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)

	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"}
	if err := config.NewJSONRepository().Set(domain.AgentCodex, ref); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	service, err := newListLifecycle()
	if err != nil {
		t.Fatalf("newListLifecycle() error = %v", err)
	}
	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "codex"})
	if err != nil {
		t.Fatalf("List(installed codex) error = %v", err)
	}
	want := cli.ListedPower{Agent: domain.AgentCodex, Power: ref}
	if len(result.Powers) != 1 || result.Powers[0] != want {
		t.Fatalf("List(installed codex) = %#v, want %#v", result, want)
	}
}

func changeToRepositoryRoot(t *testing.T) {
	t.Helper()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(originalDirectory, "..", ".."))
	if err := os.Chdir(repositoryRoot); err != nil {
		t.Fatalf("change to repository root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })
}
