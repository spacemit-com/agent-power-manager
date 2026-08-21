package local_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/cloud/local"
	"apm/internal/domain"
)

func TestLocalCloudAndCachePrepareSkill(t *testing.T) {
	realHome := t.TempDir()
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	resourcesRoot, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := local.New(resourcesRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem(client)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1"}

	stagingRoot, err := store.PrepareSkill(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}

	cachePath := filepath.Join(cacheHome, "apm", "skills", ref.Name, ref.Version)
	if stagingRoot != filepath.Join(stateHome, ".apm", "staging") {
		t.Fatalf("staging root = %q, want %q", stagingRoot, filepath.Join(stateHome, ".apm", "staging"))
	}
	stagingPath := filepath.Join(stagingRoot, "skills", ref.Name)
	versionedStagingPath := filepath.Join(stagingPath, ref.Version)
	if _, err = os.Stat(versionedStagingPath); !os.IsNotExist(err) {
		t.Fatalf("versioned staging path %q error = %v, want not exist", versionedStagingPath, err)
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
