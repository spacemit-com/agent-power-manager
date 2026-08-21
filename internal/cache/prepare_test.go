package cache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

type recordingSource struct {
	mu           sync.Mutex
	skillCalls   int
	agentCalls   int
	skillErr     error
	agentErr     error
	destinations []string
}

func (s *recordingSource) DownloadSkill(_ context.Context, _ domain.PowerRef, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillCalls++
	s.destinations = append(s.destinations, destination)
	if s.skillErr != nil {
		return s.skillErr
	}
	return os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("skill"), 0o644)
}

func (s *recordingSource) DownloadAgent(_ context.Context, _ domain.PowerRef, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentCalls++
	s.destinations = append(s.destinations, destination)
	if s.agentErr != nil {
		return s.agentErr
	}
	return os.WriteFile(destination, []byte("schema_version: 1\n"), 0o644)
}

func newTestFilesystemCache(t *testing.T, source cache.Source) *cache.FilesystemCache {
	t.Helper()
	setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func setCacheEnvironment(t *testing.T) (stateHome, cacheHome string) {
	t.Helper()
	stateHome = t.TempDir()
	cacheHome = t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	return stateHome, cacheHome
}

func TestPrepareSkillDownloadsOnceThenUsesImmutableCache(t *testing.T) {
	source := &recordingSource{}
	store := newTestFilesystemCache(t, source)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if _, err := store.PrepareSkill(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareSkill(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if source.skillCalls != 1 {
		t.Fatalf("calls = %d, want 1", source.skillCalls)
	}
}

func TestPrepareAgentDownloadsSingleFileIntoCache(t *testing.T) {
	source := &recordingSource{}
	store := newTestFilesystemCache(t, source)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	staging, err := store.PrepareAgent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "agents", "demo", "agent.yaml")); err != nil {
		t.Fatal(err)
	}
	if source.agentCalls != 1 {
		t.Fatalf("calls = %d, want 1", source.agentCalls)
	}
}

func TestPrepareSkillFailureCleansOnlyCallTemporaryDirectory(t *testing.T) {
	source := &recordingSource{skillErr: errors.New("source failed")}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	preserved := filepath.Join(cacheHome, "apm", "skills", "other", "v1.0.0", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(preserved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preserved, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareSkill(context.Background(), ref); !errors.Is(err, source.skillErr) {
		t.Fatalf("PrepareSkill() error = %v", err)
	}
	if got, err := os.ReadFile(preserved); err != nil || string(got) != "other" {
		t.Fatalf("unrelated cache entry = %q, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(cacheHome, "apm", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary versions remain: %v", entries)
	}
}

func TestPrepareSourcesReceivePrivateCacheTemporaryTargets(t *testing.T) {
	_, cacheHome := setCacheEnvironment(t)
	source := &recordingSource{}
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareSkill(context.Background(), domain.PowerRef{Name: "skill", Type: domain.PowerTypeSkill, Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), domain.PowerRef{Name: "agent", Type: domain.PowerTypeAgent, Version: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(cacheHome, "apm", ".tmp")
	for _, destination := range source.destinations {
		relative, err := filepath.Rel(wantRoot, destination)
		if err != nil || relative == ".." || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
			t.Fatalf("source destination %q is outside private temporary root %q", destination, wantRoot)
		}
	}
}

func TestPrepareSkillSameVersionConcurrencyDownloadsOnce(t *testing.T) {
	source := &recordingSource{}
	store := newTestFilesystemCache(t, source)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := store.PrepareSkill(context.Background(), ref); errs <- err }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if source.skillCalls != 1 {
		t.Fatalf("calls = %d, want 1", source.skillCalls)
	}
}

func TestNewFilesystemRejectsTypedNilSource(t *testing.T) {
	var source *recordingSource
	if store, err := cache.NewFilesystem(source); store != nil || err == nil {
		t.Fatalf("NewFilesystem() = (%v, %v), want nil/error", store, err)
	}
}
