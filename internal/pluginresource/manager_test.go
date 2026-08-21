package pluginresource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"apm/internal/cloud/local"
	"apm/internal/domain"
	"apm/internal/utils/filesystem"
)

func TestEnsureBuildsOneCompleteSharedArtifactFromLocalCloud(t *testing.T) {
	// 捕获错误变更：共享发布遗漏完整 Plugin 树或任一 marketplace。
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
	client, err := local.New(resourcesRoot)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}

	artifact, err := service.Ensure(context.Background(), ref)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	wantRoot := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "apm", "plugins", "demo-plugin", "v0.1.0")
	if artifact.Root != wantRoot || artifact.PluginPath != filepath.Join(wantRoot, "plugins", "demo-plugin") {
		t.Fatalf("Artifact paths = %q / %q, want %q / plugins/demo-plugin", artifact.Root, artifact.PluginPath, wantRoot)
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
	assertGeneratedCodexMarketplace(t, filepath.Join(artifact.Root, ".agents", "plugins", "marketplace.json"))
	assertGeneratedClaudeMarketplace(t, filepath.Join(artifact.Root, ".claude-plugin", "marketplace.json"))
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
	if plugin.Name != "demo-plugin" || plugin.Source.Kind != "local" || plugin.Source.Path != "./plugins/demo-plugin" {
		t.Fatalf("Codex marketplace plugin = %#v, want local demo-plugin", plugin)
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
	if plugin.Name != "demo-plugin" || plugin.Source != "./plugins/demo-plugin" {
		t.Fatalf("Claude marketplace plugin = %#v, want local demo-plugin", plugin)
	}
}

func TestEnsurePersistsValidatedArtifactAndReusesImmutableVersion(t *testing.T) {
	// 捕获错误变更：把已经存在的有效版本重新下载或替换。
	roots := newTestRoots(t)
	downloader := &testDownloader{}
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, downloader, testBuilders())
	ref := testPluginRef()

	artifact, err := service.Ensure(context.Background(), ref)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	wantRoot := filepath.Join(roots.cache, "plugins", "demo", "v0.1.0")
	if artifact.Root != wantRoot {
		t.Fatalf("Artifact.Root = %q, want %q", artifact.Root, wantRoot)
	}
	wantPluginPath := filepath.Join(wantRoot, "plugins", "demo")
	if artifact.PluginPath != wantPluginPath {
		t.Fatalf("Artifact.PluginPath = %q, want %q", artifact.PluginPath, wantPluginPath)
	}
	if downloader.calls != 1 {
		t.Fatalf("DownloadPlugin calls = %d, want 1", downloader.calls)
	}

	again, err := service.Ensure(context.Background(), ref)
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
	damagedRoot := filepath.Join(roots.cache, "plugins", "demo", "v0.1.0")
	if err := os.MkdirAll(filepath.Join(damagedRoot, "plugins", "demo"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() error = %v, want ErrInvalidArtifact", err)
	}
	if downloader.calls != 0 {
		t.Fatalf("DownloadPlugin calls after damaged cache = %d, want 0", downloader.calls)
	}
	if _, err := service.Lookup(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Lookup() error = %v, want ErrInvalidArtifact", err)
	}
	if downloader.calls != 0 {
		t.Fatalf("DownloadPlugin calls after Lookup = %d, want 0", downloader.calls)
	}
}

func TestStageFilesystemReturnsCacheMissWithoutRemovingExistingStaging(t *testing.T) {
	// 捕获错误变更：共享缓存缺失时仍先清理当前 staging。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	oldStaging := filepath.Join(roots.staging, "old-staging.txt")
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldStaging, []byte("preserve until a replacement exists"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := service.StageFilesystem(testPluginRef()); !errors.Is(err, ErrMiss) {
		t.Fatalf("StageFilesystem() error = %v, want ErrMiss", err)
	}
	content, err := os.ReadFile(oldStaging)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", oldStaging, err)
	}
	if got, want := string(content), "preserve until a replacement exists"; got != want {
		t.Fatalf("old staging = %q, want %q", got, want)
	}
}

func TestStageFilesystemAtomicallyRebuildsLogicalPluginLeaf(t *testing.T) {
	// 捕获错误变更：复制整个版本根、保留旧 staging，或未使用 staging 临时根原子发布。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	cacheRoot := testArtifactRoot(roots.cache, ref)
	writeValidTestArtifact(t, cacheRoot)
	cacheBefore := snapshotTestTree(t, cacheRoot)
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

	staging, err := service.StageFilesystem(ref)
	if err != nil {
		t.Fatalf("StageFilesystem() error = %v", err)
	}
	if staging != roots.staging {
		t.Fatalf("StageFilesystem() = %q, want %q", staging, roots.staging)
	}
	payload, err := os.ReadFile(filepath.Join(roots.staging, "plugins", "demo", "skills", "demo", "payload.txt"))
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
	if cacheAfter := snapshotTestTree(t, cacheRoot); cacheAfter != cacheBefore {
		t.Fatalf("immutable cache changed during staging\nbefore:\n%s\nafter:\n%s", cacheBefore, cacheAfter)
	}
}

func TestStageFilesystemPublishFailureRestoresOldCompleteStaging(t *testing.T) {
	// 捕获错误变更：发布新 staging 的 rename 失败后丢失已经完整存在的旧 staging。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(roots.staging, "complete-old-staging.txt")
	if err := os.WriteFile(old, []byte("complete old staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	publishErr := errors.New("injected staging publish failure")
	var backupRoot string
	var failedPublish bool
	service.files.rename = func(source, destination string) error {
		if source == roots.staging {
			backupRoot = destination
		}
		if destination == roots.staging && !failedPublish {
			failedPublish = true
			return publishErr
		}
		return os.Rename(source, destination)
	}

	if _, err := service.StageFilesystem(ref); !errors.Is(err, publishErr) {
		t.Fatalf("StageFilesystem() error = %v, want publish error", err)
	}
	content, err := os.ReadFile(old)
	if err != nil || string(content) != "complete old staging" {
		t.Fatalf("restored old staging = %q, %v, want complete old staging", content, err)
	}
	if _, err := os.Lstat(filepath.Join(roots.staging, "plugins", "demo", "skills", "demo", "payload.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new staging payload stat error = %v, want absent", err)
	}
	if backupRoot == "" {
		t.Fatal("old staging was not quarantined before publication")
	}
	if _, err := os.Lstat(backupRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored backup stat error = %v, want absent", err)
	}
}

func TestStageFilesystemJoinsPublishAndRestoreErrorsAndPreservesBackup(t *testing.T) {
	// 捕获错误变更：发布和恢复同时失败时丢失恢复错误，或清理仍持有旧完整 staging 的 backup。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.staging, "complete-old-staging.txt"), []byte("complete old staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	publishErr := errors.New("injected staging publish failure")
	restoreErr := errors.New("injected staging restore failure")
	var backupRoot string
	service.files.rename = func(source, destination string) error {
		switch {
		case source == roots.staging:
			backupRoot = destination
			return os.Rename(source, destination)
		case destination == roots.staging && source == backupRoot:
			return restoreErr
		case destination == roots.staging:
			return publishErr
		default:
			return os.Rename(source, destination)
		}
	}

	_, err := service.StageFilesystem(ref)
	if !errors.Is(err, publishErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("StageFilesystem() error = %v, want publish and restore errors", err)
	}
	content, readErr := os.ReadFile(filepath.Join(backupRoot, "complete-old-staging.txt"))
	if readErr != nil || string(content) != "complete old staging" {
		t.Fatalf("preserved backup = %q, %v, want complete old staging", content, readErr)
	}
}

func TestStageFilesystemBackupCleanupFailureReturnsErrorAndPreservesBackup(t *testing.T) {
	// 捕获错误变更：新 staging 发布后 backup 清理失败仍返回成功，或删除唯一旧副本。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.staging, "complete-old-staging.txt"), []byte("complete old staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("injected staging backup cleanup failure")
	var backupRoot string
	originalMkdirTemp := service.files.mkdirTemp
	service.files.mkdirTemp = func(directory, pattern string) (string, error) {
		path, err := originalMkdirTemp(directory, pattern)
		if pattern == filepath.Base(roots.staging)+"-backup-" {
			backupRoot = path
		}
		return path, err
	}
	var backupCleanupCalls int
	service.files.removeAll = func(path string) error {
		if path == backupRoot {
			backupCleanupCalls++
			if backupCleanupCalls == 2 {
				return cleanupErr
			}
		}
		return os.RemoveAll(path)
	}

	staging, err := service.StageFilesystem(ref)
	if staging != "" || !errors.Is(err, cleanupErr) {
		t.Fatalf("StageFilesystem() = %q, %v, want empty result and cleanup error", staging, err)
	}
	if _, err := os.Stat(filepath.Join(roots.staging, "plugins", "demo", "skills", "demo", "payload.txt")); err != nil {
		t.Fatalf("published new staging payload: %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(backupRoot, "complete-old-staging.txt"))
	if readErr != nil || string(content) != "complete old staging" {
		t.Fatalf("preserved cleanup-failed backup = %q, %v, want complete old staging", content, readErr)
	}
}

func TestStageFilesystemRejectsDirectCacheOverlapBeforeRecursiveCleanup(t *testing.T) {
	// 捕获错误变更：staging root 直接包含 cache 时先递归清理，再发现源已被破坏。
	roots := newTestRoots(t)
	ref := testPluginRef()
	cacheArtifact := testArtifactRoot(roots.cache, ref)
	writeValidTestArtifact(t, cacheArtifact)
	cacheBefore := snapshotTestTree(t, roots.cache)
	service := newService(
		roots.cache,
		roots.cacheTemp,
		roots.cache,
		filepath.Join(filepath.Dir(roots.cache), "safe-staging-temp"),
		&testDownloader{},
		testBuilders(),
	)
	var cleanupCalls int
	service.files.removeAll = func(path string) error {
		cleanupCalls++
		return os.RemoveAll(path)
	}

	if _, err := service.StageFilesystem(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("StageFilesystem() error = %v, want ErrInvalidArtifact", err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("recursive cleanup calls = %d, want 0 before overlap rejection", cleanupCalls)
	}
	if cacheAfter := snapshotTestTree(t, roots.cache); cacheAfter != cacheBefore {
		t.Fatalf("cache changed after direct overlap rejection\nbefore:\n%s\nafter:\n%s", cacheBefore, cacheAfter)
	}
}

func TestStageFilesystemRejectsSymlinkAliasCacheOverlapBeforeRecursiveCleanup(t *testing.T) {
	// 捕获错误变更：staging temp 经 symlink 祖先指向 artifact 时递归删除共享 cache。
	roots := newTestRoots(t)
	ref := testPluginRef()
	cacheArtifact := testArtifactRoot(roots.cache, ref)
	writeValidTestArtifact(t, cacheArtifact)
	cacheBefore := snapshotTestTree(t, roots.cache)
	cacheAlias := filepath.Join(filepath.Dir(roots.cache), "cache-alias")
	if err := os.Symlink(roots.cache, cacheAlias); err != nil {
		t.Fatal(err)
	}
	aliasedArtifact := filepath.Join(cacheAlias, "plugins", ref.Name, ref.Version)
	service := newService(
		roots.cache,
		roots.cacheTemp,
		filepath.Join(filepath.Dir(roots.cache), "safe-staging"),
		aliasedArtifact,
		&testDownloader{},
		testBuilders(),
	)
	var cleanupCalls int
	service.files.removeAll = func(path string) error {
		cleanupCalls++
		return os.RemoveAll(path)
	}

	if _, err := service.StageFilesystem(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("StageFilesystem() error = %v, want ErrInvalidArtifact", err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("recursive cleanup calls = %d, want 0 before alias rejection", cleanupCalls)
	}
	if cacheAfter := snapshotTestTree(t, roots.cache); cacheAfter != cacheBefore {
		t.Fatalf("cache changed after alias overlap rejection\nbefore:\n%s\nafter:\n%s", cacheBefore, cacheAfter)
	}
}

func TestStageFilesystemRejectsDirectStagingRootAndTempOverlapBeforeRecursiveCleanup(t *testing.T) {
	// 捕获错误变更：staging 与临时根直接相同，前置清理会删除仍需保留的旧 staging。
	roots := newTestRoots(t)
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(roots.staging, "old-staging.txt")
	if err := os.WriteFile(oldPath, []byte("old staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newService(
		roots.cache,
		roots.cacheTemp,
		roots.staging,
		roots.staging,
		&testDownloader{},
		testBuilders(),
	)
	var cleanupCalls int
	service.files.removeAll = func(path string) error {
		cleanupCalls++
		return os.RemoveAll(path)
	}

	if _, err := service.StageFilesystem(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("StageFilesystem() error = %v, want ErrInvalidArtifact", err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("recursive cleanup calls = %d, want 0 before staging overlap rejection", cleanupCalls)
	}
	contents, err := os.ReadFile(oldPath)
	if err != nil || string(contents) != "old staging" {
		t.Fatalf("old staging = %q, %v; want preserved", contents, err)
	}
}

func TestStageFilesystemRejectsSymlinkAliasBetweenStagingRootAndTempBeforeRecursiveCleanup(t *testing.T) {
	// 捕获错误变更：.tmp 祖先 symlink 使临时根物理指向旧 staging，前置 RemoveAll 会删除旧树。
	roots := newTestRoots(t)
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
	if err := os.MkdirAll(roots.staging, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(roots.staging, "old-staging.txt")
	if err := os.WriteFile(oldPath, []byte("old staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	tempParent := filepath.Dir(roots.stagingTemp)
	if err := os.Symlink(filepath.Dir(roots.staging), tempParent); err != nil {
		t.Fatal(err)
	}
	service := newService(
		roots.cache,
		roots.cacheTemp,
		roots.staging,
		roots.stagingTemp,
		&testDownloader{},
		testBuilders(),
	)
	var cleanupCalls int
	service.files.removeAll = func(path string) error {
		cleanupCalls++
		return os.RemoveAll(path)
	}

	if _, err := service.StageFilesystem(ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("StageFilesystem() error = %v, want ErrInvalidArtifact", err)
	}
	if cleanupCalls != 0 {
		t.Fatalf("recursive cleanup calls = %d, want 0 before staging alias rejection", cleanupCalls)
	}
	contents, err := os.ReadFile(oldPath)
	if err != nil || string(contents) != "old staging" {
		t.Fatalf("old staging = %q, %v; want preserved", contents, err)
	}
}

func TestEnsurePublishesOneSharedArtifactForConcurrentCallers(t *testing.T) {
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
		testBuilder{},
	)
	ref := testPluginRef()
	results := make(chan Artifact, 4)
	errs := make(chan error, 4)
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		artifact, err := service.Ensure(context.Background(), ref)
		results <- artifact
		errs <- err
	}()
	<-downloader.firstStarted
	for range 3 {
		group.Add(1)
		go func() {
			defer group.Done()
			artifact, err := service.Ensure(context.Background(), ref)
			results <- artifact
			errs <- err
		}()
	}
	runtime.Gosched()
	if downloader.started.Load() != 1 {
		t.Fatal("second download started before the first artifact was published")
	}
	close(downloader.release)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Ensure() error = %v", err)
		}
	}
	if got := downloader.started.Load(); got != 1 {
		t.Fatalf("concurrent DownloadPlugin calls = %d, want 1", got)
	}
	var root string
	for artifact := range results {
		if root == "" {
			root = artifact.Root
		}
		if artifact.Root != root {
			t.Fatalf("concurrent roots differ: %q and %q", root, artifact.Root)
		}
	}
	if _, err := service.Lookup(ref); err != nil {
		t.Fatalf("Lookup() after concurrent Ensure = %v", err)
	}
}

func TestEnsurePublishRaceConvergesThroughLookupWithoutOverwritingPublishedVersion(t *testing.T) {
	// 捕获错误变更：rename 竞态出现已发布版本时返回失败、覆盖它，或遗留本次唯一临时根。
	roots := newTestRoots(t)
	downloader := &testDownloader{}
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, downloader, testBuilders())
	ref := testPluginRef()
	renameErr := errors.New("simulated concurrent publisher")
	var temporaryRoot string
	service.files.rename = func(source, destination string) error {
		temporaryRoot = source
		if destination == "" {
			return errors.New("empty publish destination")
		}
		writeValidTestArtifact(t, destination)
		if err := os.WriteFile(filepath.Join(destination, "winner.txt"), []byte("keep winner"), 0o600); err != nil {
			t.Fatal(err)
		}
		return renameErr
	}

	artifact, err := service.Ensure(context.Background(), ref)
	if err != nil {
		t.Fatalf("Ensure() publish race error = %v", err)
	}
	if artifact.Root != testArtifactRoot(roots.cache, ref) {
		t.Fatalf("Artifact.Root = %q, want shared published root", artifact.Root)
	}
	content, err := os.ReadFile(filepath.Join(artifact.Root, "winner.txt"))
	if err != nil || string(content) != "keep winner" {
		t.Fatalf("published winner = %q, %v, want unchanged", content, err)
	}
	if _, err := os.Lstat(temporaryRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unique temporary root stat error = %v, want absent", err)
	}
	if downloader.calls != 1 {
		t.Fatalf("DownloadPlugin calls = %d, want 1", downloader.calls)
	}
}

func TestStageFilesystemSerializesSameStagingPath(t *testing.T) {
	// 捕获错误变更：同一路径的并发 staging 复制互相删除临时树或交叉发布。
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
	copier := newBlockingCopier()
	service.files.copyDir = copier.copy
	results := make(chan error, 2)
	go func() {
		_, err := service.StageFilesystem(ref)
		results <- err
	}()
	<-copier.firstStarted
	go func() {
		_, err := service.StageFilesystem(ref)
		results <- err
	}()
	runtime.Gosched()
	if got := copier.started.Load(); got != 1 {
		t.Fatalf("concurrent staging copy calls = %d, want 1 before release", got)
	}
	close(copier.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent StageFilesystem() error = %v", err)
		}
	}
	if got := copier.started.Load(); got != 2 {
		t.Fatalf("staging copy calls = %d, want 2", got)
	}
}

func TestStageFilesystemCopyFailureKeepsCompleteStagingAndCleansUniqueTemporary(t *testing.T) {
	// 捕获错误变更：复制失败时将半成品发布为 staging，或遗留/清理非本次临时目录。
	roots := newTestRoots(t)
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	ref := testPluginRef()
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, ref))
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

	if _, err := service.StageFilesystem(ref); !errors.Is(err, copyErr) {
		t.Fatalf("StageFilesystem() error = %v, want injected copy error", err)
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
	if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() typed-nil downloader error = %v, want ErrInvalidArtifact", err)
	}
	var builder *typedNilBuilder
	service = newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, builder)
	if _, err := service.Ensure(context.Background(), ref); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("Ensure() typed-nil builder error = %v, want ErrInvalidArtifact", err)
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
		if path == testArtifactRoot(roots.cache, ref) && rootChecks.Add(1) >= 4 {
			return nil, inspectErr
		}
		return os.Lstat(path)
	}

	_, err := service.Ensure(context.Background(), ref)
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

	_, err := service.Ensure(context.Background(), testPluginRef())
	if !errors.Is(err, downloadErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Ensure() error = %v, want download and cleanup errors", err)
	}
}

func TestStageFilesystemJoinsCopyAndUniqueTemporaryCleanupErrors(t *testing.T) {
	// 捕获错误变更：staging 复制失败时丢失唯一临时目录的清理错误。
	roots := newTestRoots(t)
	copyErr := errors.New("staging copy failed")
	cleanupErr := errors.New("staging temporary cleanup failed")
	service := newService(roots.cache, roots.cacheTemp, roots.staging, roots.stagingTemp, &testDownloader{}, testBuilders())
	writeValidTestArtifact(t, testArtifactRoot(roots.cache, testPluginRef()))
	service.files.copyDir = func(string, string) error { return copyErr }
	service.files.removeAll = func(path string) error {
		if filepath.Base(path) != filepath.Base(roots.stagingTemp) {
			return cleanupErr
		}
		return os.RemoveAll(path)
	}

	_, err := service.StageFilesystem(testPluginRef())
	if !errors.Is(err, copyErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("StageFilesystem() error = %v, want copy and cleanup errors", err)
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

func (downloader *testDownloader) DownloadPlugin(_ context.Context, ref domain.PowerRef, destination string) error {
	downloader.calls++
	if err := writeSharedTestPluginManifests(destination, fmt.Sprintf(`{"name":%q,"version":%q,"description":"Demo"}`, ref.Name, strings.TrimPrefix(ref.Version, "v"))); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "skills", ref.Name, "payload.txt"), []byte("payload"), 0o600)
}

type testBuilder struct{}

func (testBuilder) Build(_ domain.PowerRef, root, pluginPath string) error {
	return os.WriteFile(filepath.Join(root, "builder.txt"), []byte(pluginPath), 0o600)
}

func (testBuilder) Validate(_ domain.PowerRef, root string) error {
	if _, err := os.Stat(filepath.Join(root, "plugins", "demo", "skills", "demo", "payload.txt")); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	if _, err := os.Stat(filepath.Join(root, "builder.txt")); err != nil {
		return errors.Join(ErrInvalidArtifact, err)
	}
	return nil
}

func testBuilders() artifactBuilder {
	return testBuilder{}
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

func (downloader *blockingDownloader) DownloadPlugin(_ context.Context, ref domain.PowerRef, destination string) error {
	switch downloader.started.Add(1) {
	case 1:
		close(downloader.firstStarted)
	}
	<-downloader.release
	if err := writeSharedTestPluginManifests(destination, fmt.Sprintf(`{"name":%q,"version":%q,"description":"Demo"}`, ref.Name, strings.TrimPrefix(ref.Version, "v"))); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "skills", ref.Name, "payload.txt"), []byte("payload"), 0o600)
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

func (*typedNilBuilder) Build(domain.PowerRef, string, string) error {
	panic("typed nil builder must not be called")
}

func (*typedNilBuilder) Validate(domain.PowerRef, string) error {
	panic("typed nil builder must not be called")
}

type failingDownloader struct {
	err error
}

func (downloader failingDownloader) DownloadPlugin(context.Context, domain.PowerRef, string) error {
	return downloader.err
}
