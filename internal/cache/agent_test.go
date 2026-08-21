package cache_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

func TestPrepareAgentPublishesSingleFileAndStaging(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(&recordingSource{})
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	staging, err := store.PrepareAgent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0", "agent.yaml"),
		filepath.Join(staging, "agents", "demo", "agent.yaml"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("published Agent %q: %v", path, err)
		}
	}
}
