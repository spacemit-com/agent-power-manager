package cache_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
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
		contents = []byte(agentFixtureYAML(entry.Ref))
	}
	return writeCompleteAgentDirectory(destination, contents)
}

// agentFixtureYAML 返回与 ref 身份一致、avatar 指向 assets/avatar.png 的有效 Agent fixture。
func agentFixtureYAML(ref domain.PowerRef) string {
	return "schema_version: 1\nagent: {id: " + ref.Name + ", name: " + ref.Name + ", version: " + ref.Version + ", description: demo, soul: demo, metadata: {avatar: assets/avatar.png}}\n"
}

// testAgentAvatarPNG 是 128x128 有效 PNG，满足 avatar 校验（签名、正方形与尺寸）。
var testAgentAvatarPNG = func() []byte {
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, 128, 128))); err != nil {
		panic("encode test agent avatar PNG: " + err.Error())
	}
	return output.Bytes()
}()

// writeCompleteAgentDirectory 在 destination 下写入 agent.yaml 与 assets/avatar.png，
// 构成一个完整且有效的 Agent 目录。
func writeCompleteAgentDirectory(destination string, yamlContents []byte) error {
	if err := os.MkdirAll(filepath.Join(destination, "assets"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destination, "agent.yaml"), yamlContents, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "assets", "avatar.png"), testAgentAvatarPNG, 0o644)
}

// seedAgentCache 预置一个完整且有效的 Agent 缓存版本目录并返回其版本根路径。
func seedAgentCache(t *testing.T, cacheHome string, ref domain.PowerRef, yamlContents []byte) string {
	t.Helper()
	root := filepath.Join(cacheHome, "apm", "agents", ref.Name, ref.Version)
	if err := writeCompleteAgentDirectory(root, yamlContents); err != nil {
		t.Fatal(err)
	}
	return root
}

func cacheCatalogEntry(ref domain.PowerRef) domain.CatalogEntry {
	return domain.CatalogEntry{
		Ref: ref, Description: "catalog description", Message: "catalog message",
		ContentType: "application/gzip", ContentSize: 17,
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

func TestPrepareAgentDownloadsCompleteDirectoryIntoCache(t *testing.T) {
	source := &recordingSource{}
	store := newTestFilesystemCache(t, source)
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	entry := cacheCatalogEntry(ref)
	staging, err := store.PrepareAgent(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(staging, "agents", "demo", "agent.yaml"),
		filepath.Join(staging, "agents", "demo", "assets", "avatar.png"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("staged Agent %q: %v", path, err)
		}
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
	source := &recordingSource{agentContents: []byte(agentFixtureYAML(domain.PowerRef{Type: domain.PowerTypeAgent, Name: "agent", Version: "v1.0.0"}))}
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
	source := &recordingSource{agentContents: []byte(agentFixtureYAML(domain.PowerRef{Type: domain.PowerTypeAgent, Name: "other", Version: "v1.0.0"}))}
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

func TestPrepareAgentRejectsCacheWrongNameWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	rawYAML := []byte(agentFixtureYAML(domain.PowerRef{Type: domain.PowerTypeAgent, Name: "other", Version: "v1.0.0"}))
	root := seedAgentCache(t, cacheHome, ref, rawYAML)
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	assertCachedAgentPreserved(t, root, rawYAML)
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

// assertCachedAgentPreserved 断言缓存版本目录未被删除或改写。
func assertCachedAgentPreserved(t *testing.T, root string, wantYAML []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, "agent.yaml"))
	if err != nil || !bytes.Equal(got, wantYAML) {
		t.Fatalf("preserved cache agent.yaml = %q, %v, want %q", got, err, wantYAML)
	}
	gotAvatar, err := os.ReadFile(filepath.Join(root, "assets", "avatar.png"))
	if err != nil || !bytes.Equal(gotAvatar, testAgentAvatarPNG) {
		t.Fatalf("preserved cache avatar = %d bytes, %v, want test PNG", len(gotAvatar), err)
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
	raw := []byte("schema_version: 1\nagent: {id: demo, name: demo, version: v1.0.0, soul: demo}\n")
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

func TestPrepareAgentRejectsCacheMissingAvatarWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	raw := []byte(agentFixtureYAML(ref))
	root := filepath.Join(cacheHome, "apm", "agents", ref.Name, ref.Version)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "agent.yaml")); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("preserved cache agent.yaml = %q, %v", got, err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareAgentRejectsCacheCorruptAvatarWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	root := filepath.Join(cacheHome, "apm", "agents", ref.Name, ref.Version)
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	rawYAML := []byte(agentFixtureYAML(ref))
	if err := os.WriteFile(filepath.Join(root, "agent.yaml"), rawYAML, 0o644); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("not a png")
	if err := os.WriteFile(filepath.Join(root, "assets", "avatar.png"), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "assets", "avatar.png")); err != nil || !bytes.Equal(got, corrupt) {
		t.Fatalf("preserved cache avatar = %q, %v", got, err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareAgentRejectsCacheExtraRootFileWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	rawYAML := []byte(agentFixtureYAML(ref))
	root := seedAgentCache(t, cacheHome, ref, rawYAML)
	extra := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(extra, []byte("outside closed set"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
	}
	assertCachedAgentPreserved(t, root, rawYAML)
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("extra root file removed: %v", err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareAgentRejectsCacheSymlinkAssetWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	root := seedAgentCache(t, cacheHome, ref, []byte(agentFixtureYAML(ref)))
	realPNG := filepath.Join(t.TempDir(), "real.png")
	if err := os.WriteFile(realPNG, testAgentAvatarPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "assets", "avatar.png")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPNG, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); err == nil {
		t.Fatal("PrepareAgent() error = nil, want physical tree error")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("preserved symlink asset = %v, %v", info, err)
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}

func TestPrepareAgentFailureCleansOnlyCallTemporaryDirectory(t *testing.T) {
	source := &recordingSource{agentErr: errors.New("source failed")}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	preserved := filepath.Join(cacheHome, "apm", "agents", "other", "v1.0.0")
	if err := writeCompleteAgentDirectory(preserved, []byte(agentFixtureYAML(domain.PowerRef{Type: domain.PowerTypeAgent, Name: "other", Version: "v1.0.0"}))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref)); !errors.Is(err, source.agentErr) {
		t.Fatalf("PrepareAgent() error = %v", err)
	}
	assertCachedAgentPreserved(t, preserved, []byte(agentFixtureYAML(domain.PowerRef{Type: domain.PowerTypeAgent, Name: "other", Version: "v1.0.0"})))
	entries, err := os.ReadDir(filepath.Join(cacheHome, "apm", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary versions remain: %v", entries)
	}
}

func TestPrepareAgentUsesValidCachedIdentityWithoutCloudCall(t *testing.T) {
	source := &recordingSource{}
	_, cacheHome := setCacheEnvironment(t)
	store, err := cache.NewFilesystem(source)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	seedAgentCache(t, cacheHome, ref, []byte(agentFixtureYAML(ref)))
	staging, err := store.PrepareAgent(context.Background(), cacheCatalogEntry(ref))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(staging, "agents", "demo", "agent.yaml"),
		filepath.Join(staging, "agents", "demo", "assets", "avatar.png"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("staged Agent %q: %v", path, err)
		}
	}
	if source.agentCalls != 0 {
		t.Fatalf("agent calls = %d, want 0", source.agentCalls)
	}
}
