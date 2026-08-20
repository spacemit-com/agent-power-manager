package cache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

func TestFilesystemCacheStagesSingleAgentYAML(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}

	source := filepath.Join(cacheHome, "apm", "agents", ref.Name, ref.Version, "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	staging, err := store.StageAgent(ref)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(staging, "agents", ref.Name, "agent.yaml")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("staged agent %q missing: %v", want, err)
	}
}

func TestFilesystemCacheStageAgentMiss(t *testing.T) {
	setCacheEnvironment(t)
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "missing", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	if _, err := store.StageAgent(ref); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("StageAgent() error = %v, want ErrMiss", err)
	}
}
