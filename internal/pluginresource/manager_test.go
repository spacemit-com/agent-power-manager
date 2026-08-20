package pluginresource

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/utils/filesystem"
)

func TestLocalCloudSharedPluginPackagesBuildAgentArtifactsWithoutLegacyMarketplace(t *testing.T) {
	// 捕获错误变更：Plugin Resource 桥接回退调用旧 marketplace 索引 API。
	resourcesSource, err := filepath.Abs(filepath.Join("..", "cloud", "resources"))
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
	if err := filesystem.CopyDir(resourcesSource, resourcesRoot); err != nil {
		t.Fatalf("copy Local Cloud fixtures: %v", err)
	}
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(resourcesRoot)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}

	for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
		t.Run(string(agent), func(t *testing.T) {
			artifact, err := service.Ensure(context.Background(), agent, ref)
			if err != nil {
				t.Fatalf("Ensure() error = %v", err)
			}
			if artifact.Agent != agent || artifact.ActualName != "demo-plugin-v0.1.0" {
				t.Fatalf("Artifact identity = %#v, want %s/demo-plugin-v0.1.0", artifact, agent)
			}
			if err := artifact.Validate(); err != nil {
				t.Fatalf("Artifact.Validate() error = %v", err)
			}
			if _, err := os.Stat(filepath.Join(artifact.PluginPath, "skills", "demo-plugin", "SKILL.md")); err != nil {
				t.Fatalf("stat Plugin skill: %v", err)
			}
			for _, required := range []string{
				filepath.Join(".codex-plugin", "plugin.json"),
				filepath.Join(".claude-plugin", "plugin.json"),
				filepath.Join(".hermes-plugin", "__init__.py"),
				filepath.Join(".hermes-plugin", "plugin.yaml"),
			} {
				if _, err := os.Stat(filepath.Join(artifact.PluginPath, required)); err != nil {
					t.Fatalf("stat shared Plugin entry %q: %v", required, err)
				}
			}

			switch agent {
			case domain.AgentCodex:
				assertGeneratedCodexMarketplace(t, filepath.Join(artifact.Root, ".agents", "plugins", "marketplace.json"))
			case domain.AgentClaudeCode:
				assertGeneratedClaudeMarketplace(t, filepath.Join(artifact.Root, ".claude-plugin", "marketplace.json"))
			case domain.AgentHermes:
				for _, marketplace := range []string{
					filepath.Join(artifact.Root, ".agents", "plugins", "marketplace.json"),
					filepath.Join(artifact.Root, ".claude-plugin", "marketplace.json"),
				} {
					if _, err := os.Lstat(marketplace); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("Hermes artifact marketplace %q stat error = %v, want not exist", marketplace, err)
					}
				}
			}
		})
	}
}

func assertGeneratedCodexMarketplace(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated marketplace %q: %v", path, err)
	}
	var document struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Kind string `json:"source"`
				Path string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode generated Codex marketplace %q: %v", path, err)
	}
	if document.Name != "apm-demo-plugin" || len(document.Plugins) != 1 {
		t.Fatalf("Codex marketplace identity/count = %q/%d, want apm-demo-plugin/1", document.Name, len(document.Plugins))
	}
	plugin := document.Plugins[0]
	if plugin.Name != "demo-plugin-v0.1.0" || plugin.Source.Kind != "local" || plugin.Source.Path != "./plugins/demo-plugin-v0.1.0" {
		t.Fatalf("Codex marketplace plugin = %#v, want local demo-plugin-v0.1.0", plugin)
	}
}

func assertGeneratedClaudeMarketplace(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated marketplace %q: %v", path, err)
	}
	var document struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode generated Claude marketplace %q: %v", path, err)
	}
	if document.Name != "apm-demo-plugin" || len(document.Plugins) != 1 {
		t.Fatalf("Claude marketplace identity/count = %q/%d, want apm-demo-plugin/1", document.Name, len(document.Plugins))
	}
	plugin := document.Plugins[0]
	if plugin.Name != "demo-plugin-v0.1.0" || plugin.Source != "./plugins/demo-plugin-v0.1.0" {
		t.Fatalf("Claude marketplace plugin = %#v, want local demo-plugin-v0.1.0", plugin)
	}
}

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
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
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
	runtime.Gosched()
	if downloader.started.Load() != 1 {
		t.Fatal("second download started before the first artifact was published")
	}
	close(downloader.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Ensure() error = %v", err)
		}
	}
	if got := downloader.started.Load(); got != 1 {
		t.Fatalf("concurrent DownloadPlugin calls = %d, want 1", got)
	}
	if _, err := service.Lookup(domain.AgentCodex, ref); err != nil {
		t.Fatalf("Lookup() after concurrent Ensure = %v", err)
	}
}

func TestStageHermesSerializesSameStagingPath(t *testing.T) {
	// 捕获错误变更：同一路径的并发 staging 复制互相删除临时树或交叉发布。
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, domain.AgentHermes, ref))
	copier := newBlockingCopier()
	service.files.copyDir = copier.copy
	results := make(chan error, 2)
	go func() {
		_, err := service.StageHermes(ref)
		results <- err
	}()
	<-copier.firstStarted
	go func() {
		_, err := service.StageHermes(ref)
		results <- err
	}()
	runtime.Gosched()
	if got := copier.started.Load(); got != 1 {
		t.Fatalf("concurrent staging copy calls = %d, want 1 before release", got)
	}
	close(copier.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent StageHermes() error = %v", err)
		}
	}
	if got := copier.started.Load(); got != 2 {
		t.Fatalf("staging copy calls = %d, want 2", got)
	}
}

func TestStageHermesCopyFailureKeepsCompleteStagingAndCleansUniqueTemporary(t *testing.T) {
	// 捕获错误变更：复制失败时将半成品发布为 staging，或遗留/清理非本次临时目录。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, domain.AgentHermes, ref))
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(roots.staging, "complete-old-staging.txt")
	if err := os.WriteFile(old, []byte("complete old staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	copyErr := errors.New("injected staging copy failure")
	var temporary string
	service.files.copyDir = func(_ string, destination string) error {
		temporary = filepath.Dir(filepath.Dir(destination))
		if err := os.MkdirAll(destination, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, "partial.txt"), []byte("partial"), 0o600); err != nil {
			return err
		}
		return copyErr
	}

	if _, err := service.StageHermes(ref); !errors.Is(err, copyErr) {
		t.Fatalf("StageHermes() error = %v, want injected copy error", err)
	}
	content, err := os.ReadFile(old)
	if err != nil {
		t.Fatalf("ReadFile(old staging) error = %v", err)
	}
	if got, want := string(content), "complete old staging"; got != want {
		t.Fatalf("old staging = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(roots.staging, "plugins", "demo", "partial.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial final staging stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unique temporary stat error = %v, want not exist", err)
	}
}

func TestEnsureRejectsTypedNilDependencies(t *testing.T) {
	// 捕获错误变更：接口携带 typed-nil downloader 或 builder 时解引用 panic。
	roots := newTestRoots(t)
	ref := testPluginRef()
	var downloader *typedNilDownloader
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, downloader, testBuilders())
	if _, err := service.Ensure(context.Background(), domain.AgentCodex, ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() typed-nil downloader error = %v, want ErrInvalidArtifact", err)
	}
	var builder *typedNilBuilder
	service = newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, map[domain.AgentID]artifactBuilder{domain.AgentCodex: builder})
	if _, err := service.Ensure(context.Background(), domain.AgentCodex, ref); !errors.Is(err, ErrUnsupportedAgent) {
		t.Fatalf("Ensure() typed-nil builder error = %v, want ErrUnsupportedAgent", err)
	}
}

func TestEnsureJoinsRenameAndInspectErrors(t *testing.T) {
	// 捕获错误变更：rename 失败后的 final 检查错误被悄然丢弃。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	renameErr := errors.New("rename failed")
	inspectErr := errors.New("inspect failed")
	service.files.rename = func(string, string) error { return renameErr }
	var rootChecks atomic.Int32
	service.files.lstat = func(path string) (os.FileInfo, error) {
		if path == testArtifactRoot(roots.cache, domain.AgentCodex, ref) && rootChecks.Add(1) >= 4 {
			return nil, inspectErr
		}
		return os.Lstat(path)
	}

	_, err := service.Ensure(context.Background(), domain.AgentCodex, ref)
	if !errors.Is(err, renameErr) || !errors.Is(err, inspectErr) {
		t.Fatalf("Ensure() error = %v, want rename and inspect errors", err)
	}
}

func TestEnsureJoinsDownloadAndUniqueTemporaryCleanupErrors(t *testing.T) {
	// 捕获错误变更：下载失败时丢失同一唯一临时目录的清理错误。
	roots := newTestRoots(t)
	downloadErr := errors.New("download failed")
	cleanupErr := errors.New("temporary cleanup failed")
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, failingDownloader{err: downloadErr}, testBuilders())
	service.files.removeAll = func(string) error { return cleanupErr }

	_, err := service.Ensure(context.Background(), domain.AgentCodex, testPluginRef())
	if !errors.Is(err, downloadErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Ensure() error = %v, want download and cleanup errors", err)
	}
}

func TestStageHermesJoinsCopyAndUniqueTemporaryCleanupErrors(t *testing.T) {
	// 捕获错误变更：staging 复制失败时丢失唯一临时目录的清理错误。
	roots := newTestRoots(t)
	copyErr := errors.New("staging copy failed")
	cleanupErr := errors.New("staging temporary cleanup failed")
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, domain.AgentHermes, testPluginRef()))
	service.files.copyDir = func(string, string) error { return copyErr }
	service.files.removeAll = func(path string) error {
		if filepath.Base(path) != filepath.Base(roots.stagingTemp) {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}

	_, err := service.StageHermes(testPluginRef())
	if !errors.Is(err, copyErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("StageHermes() error = %v, want copy and cleanup errors", err)
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

func (downloader *testDownloader) DownloadPlugin(_ context.Context, _ domain.PowerRef, destination string) error {
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
	firstStarted chan struct{}
	release      chan struct{}
	started      atomic.Int32
}

func newBlockingDownloader() *blockingDownloader {
	return &blockingDownloader{
		firstStarted: make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (downloader *blockingDownloader) DownloadPlugin(_ context.Context, _ domain.PowerRef, destination string) error {
	switch downloader.started.Add(1) {
	case 1:
		close(downloader.firstStarted)
	}
	<-downloader.release
	return os.WriteFile(filepath.Join(destination, "payload.txt"), []byte("payload"), 0o600)
}

type blockingCopier struct {
	firstStarted chan struct{}
	release      chan struct{}
	started      atomic.Int32
}

func newBlockingCopier() *blockingCopier {
	return &blockingCopier{firstStarted: make(chan struct{}), release: make(chan struct{})}
}

func (copier *blockingCopier) copy(source, destination string) error {
	if copier.started.Add(1) == 1 {
		close(copier.firstStarted)
		<-copier.release
	}
	return filesystem.CopyDir(source, destination)
}

type typedNilDownloader struct{}

func (*typedNilDownloader) DownloadPlugin(context.Context, domain.PowerRef, string) error {
	panic("typed nil downloader must not be called")
}

type typedNilBuilder struct{}

func (*typedNilBuilder) Build(domain.AgentID, domain.PowerRef, string, string) error {
	panic("typed nil builder must not be called")
}

func (*typedNilBuilder) Validate(domain.AgentID, domain.PowerRef, string) error {
	panic("typed nil builder must not be called")
}

type failingDownloader struct {
	err error
}

func (downloader failingDownloader) DownloadPlugin(context.Context, domain.PowerRef, string) error {
	return downloader.err
}
