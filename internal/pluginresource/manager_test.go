package pluginresource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"apm/internal/domain"
)

func TestEnsurePersistsValidatedArtifactAndReusesImmutableVersion(t *testing.T) {
	// 捕获错误变更：把已经存在的有效版本重新下载或替换。
	roots := newTestRoots(t)
	downloader := &testDownloader{}
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, downloader, testBuilders())
	ref := testPluginRef()

	artifact, err := service.Ensure(context.Background(), domain.AgentCodex, ref)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	wantRoot := filepath.Join(roots.cache, "plugins", "codex", "demo", "v0.1.0")
	if artifact.Root != wantRoot {
		t.Fatalf("Artifact.Root = %q, want %q", artifact.Root, wantRoot)
	}
	wantPluginPath := filepath.Join(wantRoot, "plugins", "demo-v0.1.0")
	if artifact.PluginPath != wantPluginPath {
		t.Fatalf("Artifact.PluginPath = %q, want %q", artifact.PluginPath, wantPluginPath)
	}
	if downloader.calls != 1 {
		t.Fatalf("DownloadPlugin calls = %d, want 1", downloader.calls)
	}

	again, err := service.Ensure(context.Background(), domain.AgentCodex, ref)
	if err != nil {
		t.Fatalf("second Ensure() error = %v", err)
	}
	if again != artifact {
		t.Fatalf("second Artifact = %#v, want %#v", again, artifact)
	}
	if downloader.calls != 1 {
		t.Fatalf("DownloadPlugin calls after cache hit = %d, want 1", downloader.calls)
	}
}

func TestEnsureAndLookupRejectDamagedImmutableVersionWithoutDownloading(t *testing.T) {
	// 捕获错误变更：将已经存在但损坏的不可变版本当作命中，或尝试重建它。
	roots := newTestRoots(t)
	downloader := &testDownloader{}
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, downloader, testBuilders())
	ref := testPluginRef()
	damagedRoot := filepath.Join(roots.cache, "plugins", "codex", "demo", "v0.1.0")
	if err := os.MkdirAll(filepath.Join(damagedRoot, "plugins", "demo-v0.1.0"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Ensure(context.Background(), domain.AgentCodex, ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
	}
	if downloader.calls != 0 {
		t.Fatalf("DownloadPlugin calls after damaged cache = %d, want 0", downloader.calls)
	}
	if _, err := service.Lookup(domain.AgentCodex, ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
	}
	if downloader.calls != 0 {
		t.Fatalf("DownloadPlugin calls after Lookup = %d, want 0", downloader.calls)
	}
}

func TestStageHermesReturnsCacheMissWithoutRemovingExistingStaging(t *testing.T) {
	// 捕获错误变更：Hermes 缓存缺失时仍先清理当前 staging。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	oldStaging := filepath.Join(roots.staging, "old-staging.txt")
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldStaging, []byte("preserve until a replacement exists"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := service.StageHermes(testPluginRef()); !errors.Is(err, ErrMiss) {
		t.Fatalf("StageHermes() error = %v, want ErrMiss", err)
	}
	content, err := os.ReadFile(oldStaging)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", oldStaging, err)
	}
	if got, want := string(content), "preserve until a replacement exists"; got != want {
		t.Fatalf("old staging = %q, want %q", got, want)
	}
}

func TestStageHermesAtomicallyRebuildsStagingFromImmutablePluginLeaf(t *testing.T) {
	// 捕获错误变更：复制整个版本根、保留旧 staging，或未使用 staging 临时根原子发布。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	hermesRoot := testArtifactRoot(roots.cache, domain.AgentHermes, ref)
	writeValidTestArtifact(t, hermesRoot)
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.staging, "old-staging.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(roots.stagingTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.stagingTemp, "old-temp.txt"), []byte("old temp"), 0o600); err != nil {
		t.Fatal(err)
	}

	staging, err := service.StageHermes(ref)
	if err != nil {
		t.Fatalf("StageHermes() error = %v", err)
	}
	if staging != roots.staging {
		t.Fatalf("StageHermes() = %q, want %q", staging, roots.staging)
	}
	payload, err := os.ReadFile(filepath.Join(roots.staging, "plugins", "demo", "payload.txt"))
	if err != nil {
		t.Fatalf("ReadFile(staged payload) error = %v", err)
	}
	if got, want := string(payload), "payload"; got != want {
		t.Fatalf("staged payload = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(roots.staging, "old-staging.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old staging stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(roots.stagingTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging temp stat error = %v, want not exist", err)
	}
}

func TestStageHermesDoesNotChangeCodexOrClaudeArtifactRoots(t *testing.T) {
	// 捕获错误变更：重建 Hermes staging 时误清理或移动其他 Agent 的缓存。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, domain.AgentHermes, ref))
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		root := testArtifactRoot(roots.cache, agent, ref)
		writeValidTestArtifact(t, root)
		if err := os.WriteFile(filepath.Join(root, "agent-sentinel.txt"), []byte(string(agent)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := service.StageHermes(ref); err != nil {
		t.Fatalf("StageHermes() error = %v", err)
	}
	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode} {
		path := filepath.Join(testArtifactRoot(roots.cache, agent, ref), "agent-sentinel.txt")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		if got, want := string(content), string(agent); got != want {
			t.Fatalf("artifact sentinel for %s = %q, want %q", agent, got, want)
		}
	}
}

func TestEnsureConcurrentPublishersReturnTheValidatedImmutableArtifact(t *testing.T) {
	// 捕获错误变更：同一 Service 并发发布同版本，在第一个版本发布前启动第二次下载。
	roots := newTestRoots(t)
	downloader := newBlockingDownloader()
	service := newService(
		roots.cache,
		roots.cacheTemp,
		roots.staging,
		roots.stagingTemp,
		downloader,
		map[domain.AgentID]artifactBuilder{domain.AgentCodex: testBuilder{}},
	)
	ref := testPluginRef()
	results := make(chan error, 2)
	go func() {
		_, err := service.Ensure(context.Background(), domain.AgentCodex, ref)
		results <- err
	}()
	<-downloader.firstStarted
	go func() {
		_, err := service.Ensure(context.Background(), domain.AgentCodex, ref)
		results <- err
	}()
	select {
	case <-downloader.secondStarted:
		t.Fatal("second download started before the first artifact was published")
	case <-time.After(100 * time.Millisecond):
	}
	close(downloader.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Ensure() error = %v", err)
		}
	}
	if downloader.calls != 1 {
		t.Fatalf("concurrent DownloadPlugin calls = %d, want 1", downloader.calls)
	}
	if _, err := service.Lookup(domain.AgentCodex, ref); err != nil {
		t.Fatalf("Lookup() after concurrent Ensure = %v", err)
	}
}

type testRoots struct {
	cache       string
	cacheTemp   string
	staging     string
	stagingTemp string
}

func newTestRoots(t *testing.T) testRoots {
	t.Helper()
	root := t.TempDir()
	return testRoots{
		cache:       filepath.Join(root, "cache"),
		cacheTemp:   filepath.Join(root, "cache", ".tmp"),
		staging:     filepath.Join(root, "staging"),
		stagingTemp: filepath.Join(root, ".tmp", "staging"),
	}
}

func testPluginRef() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
}

type testDownloader struct {
	calls int
}

func (downloader *testDownloader) DownloadPlugin(_ context.Context, _ domain.AgentID, _ domain.PowerRef, destination string) error {
	downloader.calls++
	return os.WriteFile(filepath.Join(destination, "payload.txt"), []byte("payload"), 0o600)
}

type testBuilder struct{}

func (testBuilder) Build(_ domain.AgentID, _ domain.PowerRef, root, pluginPath string) error {
	return os.WriteFile(filepath.Join(root, "builder.txt"), []byte(pluginPath), 0o600)
}

func (testBuilder) Validate(_ domain.AgentID, _ domain.PowerRef, root string) error {
	if _, err := os.Stat(filepath.Join(root, "plugins", "demo-v0.1.0", "payload.txt")); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if _, err := os.Stat(filepath.Join(root, "builder.txt")); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

func testBuilders() map[domain.AgentID]artifactBuilder {
	return map[domain.AgentID]artifactBuilder{
		domain.AgentCodex:      testBuilder{},
		domain.AgentClaudeCode: testBuilder{},
		domain.AgentHermes:     testBuilder{},
	}
}

type blockingDownloader struct {
	firstStarted  chan struct{}
	secondStarted chan struct{}
	release       chan struct{}
	calls         int
}

func newBlockingDownloader() *blockingDownloader {
	return &blockingDownloader{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		release:       make(chan struct{}),
	}
}

func (downloader *blockingDownloader) DownloadPlugin(_ context.Context, _ domain.AgentID, _ domain.PowerRef, destination string) error {
	downloader.calls++
	switch downloader.calls {
	case 1:
		close(downloader.firstStarted)
	case 2:
		close(downloader.secondStarted)
	}
	<-downloader.release
	return os.WriteFile(filepath.Join(destination, "payload.txt"), []byte("payload"), 0o600)
}
