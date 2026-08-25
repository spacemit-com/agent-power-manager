package cache_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

type recordingSource struct {
	mu            sync.Mutex
	skillCalls    int
	agentCalls    int
	skillErr      error
	agentErr      error
	agentContents []byte
	destinations  []string
	skillEntries  []domain.CatalogEntry
	agentEntries  []domain.CatalogEntry
}

func (s *recordingSource) DownloadSkill(_ context.Context, entry domain.CatalogEntry, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skillCalls++
	s.destinations = append(s.destinations, destination)
	s.skillEntries = append(s.skillEntries, entry.Clone())
	if s.skillErr != nil {
		return s.skillErr
	}
	contents := []byte("---\nname: " + entry.Ref.Name + "\nversion: \"" + entry.Ref.Version + "\"\ndescription: skill\n---\n")
	return os.WriteFile(filepath.Join(destination, "SKILL.md"), contents, 0o644)
}

func (s *recordingSource) DownloadAgent(_ context.Context, entry domain.CatalogEntry, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentCalls++
	s.destinations = append(s.destinations, destination)
	s.agentEntries = append(s.agentEntries, entry.Clone())
	if s.agentErr != nil {
		return s.agentErr
	}
	contents := s.agentContents
	if len(contents) == 0 {
		contents = []byte("schema_version: 1\nagent: {id: " + entry.Ref.Name + ", version: " + entry.Ref.Version + ", description: demo}\n")
	}
	return os.WriteFile(destination, contents, 0o644)
}

func cacheCatalogEntry(ref domain.PowerRef) domain.CatalogEntry {
	contentType := "application/gzip"
	if ref.Type == domain.PowerTypeAgent {
		contentType = "application/yaml"
	}
	return domain.CatalogEntry{
		Ref: ref, Description: "catalog description", Message: "catalog message",
		ContentType: contentType, ContentSize: 17,
		ContentMD5:  "0123456789abcdef0123456789abcdef",
		PublishedAt: "2026-08-25T12:34:56Z", Tags: []string{"catalog-tag"},
	}
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
	entry := cacheCatalogEntry(ref)
	if _, err := store.PrepareSkill(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareSkill(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if source.skillCalls != 1 {
		t.Fatalf("calls = %d, want 1", source.skillCalls)
	}
	if len(source.skillEntries) != 1 || !reflect.DeepEqual(source.skillEntries[0], entry) {
		t.Fatalf("DownloadSkill entries = %#v, want complete entry %#v once", source.skillEntries, entry)
	}
}

func TestPrepareAgentDownloadsSingleFileIntoCache(t *testing.T) {
	source := &recordingSource{}
	store := newTestFilesystemCache(t, source)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	entry := cacheCatalogEntry(ref)
	staging, err := store.PrepareAgent(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "agents", "demo", "agent.yaml")); err != nil {
		t.Fatal(err)
	}
	if source.agentCalls != 1 {
		t.Fatalf("calls = %d, want 1", source.agentCalls)
	}
	if len(source.agentEntries) != 1 || !reflect.DeepEqual(source.agentEntries[0], entry) {
		t.Fatalf("DownloadAgent entries = %#v, want complete entry %#v once", source.agentEntries, entry)
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
	if _, err := store.PrepareSkill(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, source.skillErr) {
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
	source := &recordingSource{agentContents: []byte("schema_version: 1\nagent: {id: agent, version: v1.0.0, description: agent}\n")}
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareSkill(context.Background(), cacheCatalogEntry(domain.PowerRef{Name: "skill", Type: domain.PowerTypeSkill, Version: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(domain.PowerRef{Name: "agent", Type: domain.PowerTypeAgent, Version: "v1.0.0"})); err != nil {
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
		go func() { _, err := store.PrepareSkill(context.Background(), cacheCatalogEntry(ref)); errs <- err }()
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

func TestPrepareRejectsUnsafeOrNoncanonicalReferenceBeforeCacheHit(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*cache.FilesystemCache, context.Context, domain.CatalogEntry) (string, error)
		ref     domain.PowerRef
		marker  string
		path    []string
	}{
		{name: "Skill traversal", prepare: (*cache.FilesystemCache).PrepareSkill, ref: domain.PowerRef{Name: "../escaped", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, marker: "SKILL.md", path: []string{"escaped", "v1.0.0"}},
		{name: "Skill noncanonical version", prepare: (*cache.FilesystemCache).PrepareSkill, ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}, marker: "SKILL.md", path: []string{"skills", "demo", "1.0.0"}},
		{name: "Agent traversal", prepare: (*cache.FilesystemCache).PrepareAgent, ref: domain.PowerRef{Name: "../escaped", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, marker: "agent.yaml", path: []string{"escaped", "v1.0.0"}},
		{name: "Agent noncanonical version", prepare: (*cache.FilesystemCache).PrepareAgent, ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "1.0.0"}, marker: "agent.yaml", path: []string{"agents", "demo", "1.0.0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, cacheHome := setCacheEnvironment(t)
			source := &recordingSource{}
			store, err := cache.NewFilesystem(source)
			if err != nil {
				t.Fatal(err)
			}
			trap := filepath.Join(append([]string{cacheHome, "apm"}, test.path...)...)
			if err := os.MkdirAll(trap, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(trap, test.marker), []byte("trap"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := test.prepare(store, context.Background(), cacheCatalogEntry(test.ref)); err == nil {
				t.Fatal("Prepare() error = nil, want exact-reference validation error")
			}
			if source.skillCalls != 0 || source.agentCalls != 0 {
				t.Fatalf("source calls = skill:%d agent:%d, want zero", source.skillCalls, source.agentCalls)
			}
		})
	}
}

func TestNewFilesystemRejectsTypedNilSource(t *testing.T) {
	var source *recordingSource
	if store, err := cache.NewFilesystem(source); store != nil || err == nil {
		t.Fatalf("NewFilesystem() = (%v, %v), want nil/error", store, err)
	}
}

func TestPrepareAgentRejectsDownloadedIdentityBeforeCachePublication(t *testing.T) {
	source := &recordingSource{agentContents: []byte("schema_version: 1\nagent: {id: other, version: v1.0.0, description: other}\n")}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	if _, err := os.Lstat(filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published invalid cache: %v", err)
	}
}

func TestPrepareAgentKeepsInvalidCacheAndDoesNotCallCloud(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema_version: 1\nagent: {id: demo, version: v2.0.0, description: demo}\n")
	if err := os.WriteFile(cached, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	got, err := os.ReadFile(cached)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("preserved cache = %q, %v", got, err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareSkillRejectsCachedIdentityMismatchWithoutProviderCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}
	cached := filepath.Join(cacheHome, "apm", "skills", ref.Name, ref.Version, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nname: other\nversion: \"v1.0.0\"\ndescription: other\n---\n")
	if err := os.WriteFile(cached, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.PrepareSkill(context.Background(), cacheCatalogEntry(ref)); err == nil {
		t.Fatal("PrepareSkill() error = nil, want cached identity mismatch")
	}
	if source.skillCalls != 0 {
		t.Fatalf("DownloadSkill calls = %d, want zero on invalid immutable cache", source.skillCalls)
	}
	if got, err := os.ReadFile(cached); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("cached mismatch bytes = %q, %v, want preserved", got, err)
	}
}

func TestPrepareAgentRejectsMalformedCachedYAMLWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema_version: [\n")
	if err := os.WriteFile(cached, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	got, err := os.ReadFile(cached)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("preserved cache = %q, %v", got, err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareAgentRejectsCachedAgentMissingDescription(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0}\n")
	if err := os.WriteFile(cached, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareAgentUsesValidCachedIdentityWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	if err := os.WriteFile(cached, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	staging, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staging, "agents", "demo", "agent.yaml")); err != nil {
		t.Fatal(err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}
