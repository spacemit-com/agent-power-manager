package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

func TestPrepareSkillPublishesCacheAndStaging(t *testing.T) {
	stateHome, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(&recordingSource{})
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	staging, err := store.PrepareSkill(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(cacheHome, "apm", "skills", "demo", "v1.0.0", "SKILL.md"),
		filepath.Join(staging, "skills", "demo", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("published Skill %q: %v", path, err)
		}
	}
	if want := filepath.Join(stateHome, ".apm", "staging"); staging != want {
		t.Fatalf("staging = %q, want %q", staging, want)
	}
}
