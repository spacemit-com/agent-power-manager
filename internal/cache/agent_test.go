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

func TestPrepareAgentDownloadsMatchingIdentity(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	source := &recordingSource{agentContents: []byte("schema_version: 1\nagent: {id: custom, version: v2.3.4, description: custom}\n")}
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "custom", Version: "v2.3.4"}
	staging, err := store.PrepareAgent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(cacheHome, "apm", "agents", "custom", "v2.3.4", "agent.yaml"),
		filepath.Join(staging, "agents", "custom", "agent.yaml"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("published Agent %q: %v", path, err)
		}
	}
	if source.agentCalls != 1 {
		t.Fatalf("agent calls = %d, want 1", source.agentCalls)
	}
}
