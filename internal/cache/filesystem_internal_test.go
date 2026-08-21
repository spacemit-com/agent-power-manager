package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

type stagingFailureSource struct{}

func (stagingFailureSource) DownloadSkill(_ context.Context, ref domain.PowerRef, destination string) error {
	return os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("skill-"+ref.Version), 0o644)
}

func (stagingFailureSource) DownloadAgent(_ context.Context, ref domain.PowerRef, destination string) error {
	return os.WriteFile(destination, []byte("agent-"+ref.Version), 0o644)
}

func newInternalFilesystemCache(t *testing.T) *FilesystemCache {
	t.Helper()
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	store, err := NewFilesystem(stagingFailureSource{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPrepareSkillStagingFailureRetainsPreviousStaging(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	store.copyDir = func(string, string) error { return errors.New("copy staging candidate") }
	if _, err := store.PrepareSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}); err == nil {
		t.Fatal("PrepareSkill() error = nil, want staging copy error")
	}
	if got, err := os.ReadFile(filepath.Join(store.stagingPath, "skills", "demo", "SKILL.md")); err != nil || string(got) != "skill-v1.0.0" {
		t.Fatalf("previous staging = %q, %v, want skill-v1.0.0", got, err)
	}
}

func TestPrepareAgentStagingFailureRetainsPreviousStaging(t *testing.T) {
	store := newInternalFilesystemCache(t)
	if _, err := store.PrepareAgent(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	store.readFile = func(string) ([]byte, error) { return nil, errors.New("read staging candidate") }
	if _, err := store.PrepareAgent(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v2.0.0"}); err == nil {
		t.Fatal("PrepareAgent() error = nil, want staging read error")
	}
	if got, err := os.ReadFile(filepath.Join(store.stagingPath, "agents", "demo", "agent.yaml")); err != nil || string(got) != "agent-v1.0.0" {
		t.Fatalf("previous staging = %q, %v, want agent-v1.0.0", got, err)
	}
}
