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

func TestNewInstallLifecycleInstallsBundledSkillAndThenReturnsUnchanged(t *testing.T) {
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
	service, err := newInstallLifecycle()
	if err != nil {
		t.Fatalf("newInstallLifecycle() error = %v", err)
	}
	flags := cli.Flags{Agent: "codex", Name: "foo-before-summary"}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.0.1"}

	installed, err := service.Install(context.Background(), flags)
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	wantInstalled := cli.InstallResult{Items: []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   ref,
		Outcome: cli.InstallOutcomeInstalled,
	}}}
	if len(installed.Items) != len(wantInstalled.Items) || installed.Items[0] != wantInstalled.Items[0] {
		t.Fatalf("first Install() = %#v, want %#v", installed, wantInstalled)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", ref.Name, "SKILL.md")); err != nil {
		t.Fatalf("installed skill marker: %v", err)
	}
	manifested, found, err := config.NewJSONRepository().Find(domain.AgentCodex, ref.Type, ref.Name)
	if err != nil {
		t.Fatalf("find installed manifest = %v", err)
	}
	if !found || manifested != ref {
		t.Fatalf("installed manifest = (%#v, %t), want (%#v, true)", manifested, found, ref)
	}
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", ".tmp", "staging"))

	unchanged, err := service.Install(context.Background(), flags)
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	wantUnchanged := cli.InstallResult{Items: []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   ref,
		Outcome: cli.InstallOutcomeUnchanged,
	}}}
	if len(unchanged.Items) != len(wantUnchanged.Items) || unchanged.Items[0] != wantUnchanged.Items[0] {
		t.Fatalf("second Install() = %#v, want %#v", unchanged, wantUnchanged)
	}
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", ".tmp", "staging"))
}

func TestNewInstallLifecycleRejectsAllWhenNoAgentsAreInstalled(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)

	service, err := newInstallLifecycle()
	if err != nil {
		t.Fatalf("newInstallLifecycle() error = %v", err)
	}
	result, err := service.Install(context.Background(), cli.Flags{Agent: "all", Name: "foo-before-summary"})
	if err == nil {
		t.Fatal("Install(all) error = nil, want no installed Agents error")
	}
	if len(result.Items) != 0 {
		t.Fatalf("Install(all) result = %#v, want zero items", result)
	}

	assertPathAbsent(t, filepath.Join(cacheHome, "apm"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "manifests"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", ".tmp", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions"))
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("path %q exists or cannot be inspected: %v", path, err)
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
