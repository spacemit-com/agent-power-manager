package cache_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

func TestFilesystemCacheRejectsEmptyVersion(t *testing.T) {
	stateHome, _ := setCacheEnvironment(t)
	stagingFile := filepath.Join(stateHome, ".apm", "staging", "old.txt")
	if err := os.MkdirAll(filepath.Dir(stagingFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagingFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Fetch(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill})
	if err == nil || errors.Is(err, cache.ErrMiss) {
		t.Fatalf("Fetch() error = %v, want non-sentinel parameter error", err)
	}
	if got, err := os.ReadFile(stagingFile); err != nil || string(got) != "keep" {
		t.Fatalf("existing staging after empty-version Fetch() = %q, %v; want preserved", got, err)
	}
}

func TestFilesystemCacheMissClearsOldStagingAndTemporaryStaging(t *testing.T) {
	stateHome, _ := setCacheEnvironment(t)
	staleStagingPaths := writeStaleStaging(t, stateHome)
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Fetch(domain.PowerRef{Name: "missing", Type: domain.PowerTypeSkill, Version: "1.0.0"})
	if !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("Fetch() error = %v, want ErrMiss", err)
	}
	assertStagingRemoved(t, staleStagingPaths)
}

func TestFilesystemCacheFetchesVersionWithoutStagingVersionLayer(t *testing.T) {
	stateHome, cacheHome := setCacheEnvironment(t)
	source := filepath.Join(cacheHome, "apm", "skills", "demo", "1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skill-card.md"), []byte("card"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	content, err := store.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(stateHome, ".apm", "staging", "skills", "demo")
	if content.Path != wantPath {
		t.Fatalf("Content.Path = %q, want %q", content.Path, wantPath)
	}
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if _, err = os.Stat(filepath.Join(wantPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(stateHome, ".apm", ".tmp", "staging")); !os.IsNotExist(err) {
		t.Fatalf("staging temp remains: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "SKILL.md")); err != nil || string(got) != "skill" {
		t.Fatalf("cache source changed: %q, %v", got, err)
	}
}

func TestFilesystemCacheUnsupportedTypesMissAndClearStagingAndTemporaryStaging(t *testing.T) {
	for _, powerType := range []domain.PowerType{domain.PowerTypePlugin, domain.PowerTypeMCP} {
		t.Run(string(powerType), func(t *testing.T) {
			stateHome, _ := setCacheEnvironment(t)
			staleStagingPaths := writeStaleStaging(t, stateHome)
			store, err := cache.NewFilesystem()
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.Fetch(domain.PowerRef{Name: "demo", Type: powerType, Version: "1.0.0"})
			if !errors.Is(err, cache.ErrMiss) {
				t.Fatalf("Fetch() error = %v, want ErrMiss", err)
			}
			assertStagingRemoved(t, staleStagingPaths)
		})
	}
}

func TestFilesystemCachePreservesSourceFilesystemError(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	blockedParent := filepath.Join(cacheHome, "apm", "skills", "demo")
	if err := os.MkdirAll(filepath.Dir(blockedParent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(blockedParent, "1.0.0")
	_, err = store.Fetch(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"})
	if err == nil {
		t.Fatal("Fetch() error = nil, want cache source filesystem error")
	}
	if errors.Is(err, cache.ErrMiss) {
		t.Fatalf("Fetch() error = %v, must not be ErrMiss", err)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("Fetch() error = %v, want ENOTDIR", err)
	}
	if !strings.Contains(err.Error(), source) {
		t.Fatalf("Fetch() error = %q, want source path %q", err, source)
	}
}

func setCacheEnvironment(t *testing.T) (stateHome, cacheHome string) {
	t.Helper()
	stateHome = t.TempDir()
	cacheHome = t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	return stateHome, cacheHome
}

func writeStaleStaging(t *testing.T, stateHome string) []string {
	t.Helper()
	paths := []string{
		filepath.Join(stateHome, ".apm", "staging", "old.txt"),
		filepath.Join(stateHome, ".apm", ".tmp", "staging", "old.tmp"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func assertStagingRemoved(t *testing.T, stalePaths []string) {
	t.Helper()
	for _, path := range stalePaths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale staging path %q remains: %v", path, err)
		}
	}
}
