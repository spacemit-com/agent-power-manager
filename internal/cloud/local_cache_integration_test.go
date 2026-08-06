package cloud_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalCloudAndCacheFetchGetFetch(t *testing.T) {
	realHome := t.TempDir()
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	resourcesRoot, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(resourcesRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "0.0.1"}

	if _, err = store.Fetch(ref); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("first Fetch() error = %v, want ErrMiss", err)
	}
	if err = client.Get(ref); err != nil {
		t.Fatal(err)
	}
	content, err := store.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}

	cachePath := filepath.Join(cacheHome, "apm", "skills", ref.Name, ref.Version)
	stagingPath := filepath.Join(stateHome, ".apm", "staging", "skills", ref.Name)
	if content.Path != stagingPath {
		t.Fatalf("Content.Path = %q, want %q", content.Path, stagingPath)
	}
	for _, root := range []string{cachePath, stagingPath} {
		for _, name := range []string{"SKILL.md", "skill-card.md"} {
			if _, err = os.Stat(filepath.Join(root, name)); err != nil {
				t.Fatalf("stat %s/%s: %v", root, name, err)
			}
		}
	}
	if entries, err := os.ReadDir(realHome); err != nil || len(entries) != 0 {
		t.Fatalf("HOME touched: entries=%v err=%v", entries, err)
	}
}
