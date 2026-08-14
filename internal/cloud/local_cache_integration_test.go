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

func TestLocalCloudAndCacheStageDownloadStageSkill(t *testing.T) {
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
	ref := domain.PowerRef{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1"}

	if _, err = store.StageSkill(ref); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("first staging call error = %v, want ErrMiss", err)
	}
	if err = client.DownloadSkill(ref); err != nil {
		t.Fatal(err)
	}
	stagingRoot, err := store.StageSkill(ref)
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

func TestLocalCloudAndCacheStageDownloadStagePlugin(t *testing.T) {
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
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"}
	if _, err = store.StagePlugin(ref); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("first staging call error = %v, want ErrMiss", err)
	}
	if err = client.DownloadPlugin(ref); err != nil {
		t.Fatal(err)
	}
	stagingRoot, err := store.StagePlugin(ref)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheHome, "apm", "plugins", ref.Name, ref.Version)
	stagingPath := filepath.Join(stagingRoot, "plugins", ref.Name)
	for _, root := range []string{cachePath, stagingPath} {
		for _, name := range []string{
			filepath.Join(".codex-plugin", "plugin.json"),
			filepath.Join(".claude-plugin", "plugin.json"),
			filepath.Join("skills", "demo-plugin", "SKILL.md"),
		} {
			if _, err := os.Stat(filepath.Join(root, name)); err != nil {
				t.Fatalf("stat %s/%s: %v", root, name, err)
			}
		}
	}
	if entries, err := os.ReadDir(realHome); err != nil || len(entries) != 0 {
		t.Fatalf("HOME touched: entries=%v err=%v", entries, err)
	}
}
