package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"apm/internal/cache"
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lifecycle"
	"apm/internal/logging"
	"apm/internal/pluginresource"
)

func useBundledLocalCloud(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "internal", "cloud", "resources"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APM_CLOUD_PROVIDER", "local")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", root)
	t.Setenv("APC_BASE_URL", "")
}

func newBundledCLIEntry(t *testing.T, stdout, stderr *bytes.Buffer) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	composition := newCatalogLifecycleComposition()
	return cli.New(
		stdout,
		stderr,
		func(verbose bool) { slog.SetDefault(logging.NewLogger(stdout, stderr, verbose)) },
		composition.newSearchLifecycle,
		composition.newListLifecycle,
		composition.newInstallLifecycle,
		newUninstallLifecycle,
		newFixLifecycle,
		newValidateLifecycle,
		newSubmitLifecycle,
		newSubmissionQueryLifecycle,
		composition.newShowLifecycle,
	)
}

type recordingPluginPackageDownloader struct {
	ctx         context.Context
	entry       domain.CatalogEntry
	ref         domain.PowerRef
	destination string
	err         error
	calls       int
}

func (downloader *recordingPluginPackageDownloader) DownloadPlugin(ctx context.Context, entry domain.CatalogEntry, destination string) error {
	downloader.calls++
	downloader.ctx = ctx
	downloader.entry = entry.Clone()
	downloader.ref = entry.Ref
	downloader.destination = destination
	return downloader.err
}

func TestNewPluginResourcesRejectsNilDownloaderAtConstruction(t *testing.T) {
	var typedNil *recordingPluginPackageDownloader
	tests := []struct {
		name   string
		source interface {
			DownloadPlugin(context.Context, domain.CatalogEntry, string) error
		}
	}{
		{name: "nil"},
		{name: "typed nil", source: typedNil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, err := newPluginResources(test.source)
			if manager != nil || err == nil {
				t.Fatalf("newPluginResources() = (%v, %v), want nil/error", manager, err)
			}
		})
	}
}

func TestNewPluginResourcesCreatesDistinctManagerPerInstallFactoryCall(t *testing.T) {
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	downloader := &recordingPluginPackageDownloader{}
	first, err := newPluginResources(downloader)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newPluginResources(downloader)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("newPluginResources() reused a mutable manager across factory calls")
	}
}

func TestInstallLifecycleCompositionOpensOneCloudClientPerInvocation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	client := &recordingCloudClient{}
	calls := 0
	service, err := newInstallLifecycleWithCloudFactory(func() (cloud.Client, error) {
		calls++
		return client, nil
	})
	if err != nil {
		t.Fatalf("compose lifecycle: %v", err)
	}
	if service == nil {
		t.Fatal("composed lifecycle = nil")
	}
	if calls != 1 {
		t.Fatalf("Cloud factory calls = %d, want 1", calls)
	}
}

func TestUninstallCompositionIgnoresInvalidCloudConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		baseURL   string
		localRoot string
	}{
		{name: "provider", provider: "unsupported"},
		{name: "remote base URL", provider: "remote", baseURL: "://invalid"},
		{name: "Local resource root", provider: "local", localRoot: "relative/resources"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APM_HOME", root)
			t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
			t.Setenv("APM_CLOUD_PROVIDER", test.provider)
			t.Setenv("APC_BASE_URL", test.baseURL)
			t.Setenv("APM_CLOUD_LOCAL_ROOT", test.localRoot)

			service, err := newUninstallLifecycle()
			if err != nil || service == nil {
				t.Fatalf("newUninstallLifecycle() = (%T, %v), want local-only lifecycle", service, err)
			}
		})
	}
}

type recordingCatalogManager struct {
	snapshot      catalog.Snapshot
	snapshotCalls int
	lockedCalls   int
}

type typedNilCatalogManager struct{}

var typedNilCatalogManagerSnapshotCalls int

func (*typedNilCatalogManager) Snapshot(context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	typedNilCatalogManagerSnapshotCalls++
	return catalog.Snapshot{}, catalog.SyncStatus{}, nil
}

func (*typedNilCatalogManager) SnapshotLocked(context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	typedNilCatalogManagerSnapshotCalls++
	return catalog.Snapshot{}, catalog.SyncStatus{}, nil
}

func (manager *recordingCatalogManager) Snapshot(context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	manager.snapshotCalls++
	return manager.snapshot, catalog.SyncStatus{}, nil
}

func (manager *recordingCatalogManager) SnapshotLocked(context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	manager.lockedCalls++
	return manager.snapshot, catalog.SyncStatus{}, nil
}

func TestCatalogLifecycleCompositionSharesOneLazyManager(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("CODEX_HOME", "")
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)
	if err := os.Mkdir(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	manager := &recordingCatalogManager{snapshot: mainTestCatalogSnapshot(t)}
	managerFactoryCalls := 0
	composition := newCatalogLifecycleCompositionWithManagerFactory(func() (lifecycle.CatalogManager, error) {
		managerFactoryCalls++
		return manager, nil
	})

	search, err := composition.newSearchLifecycle()
	if err != nil {
		t.Fatalf("compose search lifecycle: %v", err)
	}
	list, err := composition.newListLifecycle()
	if err != nil {
		t.Fatalf("compose list lifecycle: %v", err)
	}
	install, err := composition.newInstallLifecycle()
	if err != nil {
		t.Fatalf("compose install lifecycle: %v", err)
	}
	if managerFactoryCalls != 0 {
		t.Fatalf("manager factory calls after construction = %d, want 0", managerFactoryCalls)
	}
	if _, err := search.Search(context.Background(), cli.Flags{Keyword: "demo"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if _, err := list.List(context.Background(), cli.Flags{Type: "skill"}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if _, err := install.Install(context.Background(), cli.Flags{Agent: "codex", Name: "missing-power"}); err == nil {
		t.Fatal("Install(missing-power) error = nil, want catalog resolution failure")
	}
	if managerFactoryCalls != 1 || manager.snapshotCalls != 2 || manager.lockedCalls != 1 {
		t.Fatalf("manager factory/Snapshot/SnapshotLocked calls = %d/%d/%d, want 1/2/1", managerFactoryCalls, manager.snapshotCalls, manager.lockedCalls)
	}
}

func TestCatalogLifecycleCompositionDefersManagerForInstalledList(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	managerFactoryCalls := 0
	composition := newCatalogLifecycleCompositionWithManagerFactory(func() (lifecycle.CatalogManager, error) {
		managerFactoryCalls++
		return nil, errors.New("invalid catalog source configuration")
	})

	list, err := composition.newListLifecycle()
	if err != nil {
		t.Fatalf("compose list lifecycle: %v", err)
	}
	result, err := list.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
	if err != nil || len(result.Powers) != 0 {
		t.Fatalf("List(installed) = %#v, %v, want an empty successful result", result, err)
	}
	if managerFactoryCalls != 0 {
		t.Fatalf("manager/source/lock factory calls = %d, want 0", managerFactoryCalls)
	}
}

func TestListInstalledOmittingAgentListsAllSupportedAgents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	composition := newCatalogLifecycleCompositionWithManagerFactory(func() (lifecycle.CatalogManager, error) {
		return nil, errors.New("invalid catalog source configuration")
	})

	list, err := composition.newListLifecycle()
	if err != nil {
		t.Fatalf("compose list lifecycle: %v", err)
	}
	result, err := list.List(context.Background(), cli.Flags{Installed: true})
	if err != nil || len(result.Powers) != 0 {
		t.Fatalf("List(installed, omitted agent) = %#v, %v, want empty powers without error", result, err)
	}
	if len(result.Agents) != 4 {
		t.Fatalf("result.Agents = %#v, want 4 supported agents", result.Agents)
	}
	for _, summary := range result.Agents {
		if summary.State != cli.InstalledAgentNotInstalled {
			t.Fatalf("summary = %#v, want not-installed", summary)
		}
	}
}

func TestCatalogLifecycleCompositionRejectsTypedNilManagerAcrossListAndSearch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	typedNilCatalogManagerSnapshotCalls = 0
	var typedNil *typedNilCatalogManager
	managerFactoryCalls := 0
	composition := newCatalogLifecycleCompositionWithManagerFactory(func() (lifecycle.CatalogManager, error) {
		managerFactoryCalls++
		return typedNil, nil
	})
	search, err := composition.newSearchLifecycle()
	if err != nil {
		t.Fatalf("compose search lifecycle: %v", err)
	}
	list, err := composition.newListLifecycle()
	if err != nil {
		t.Fatalf("compose list lifecycle: %v", err)
	}

	operations := []struct {
		name string
		call func() error
	}{
		{name: "first search", call: func() error { _, err := search.Search(context.Background(), cli.Flags{Keyword: "demo"}); return err }},
		{name: "first list", call: func() error { _, err := list.List(context.Background(), cli.Flags{}); return err }},
		{name: "repeat search", call: func() error { _, err := search.Search(context.Background(), cli.Flags{Keyword: "demo"}); return err }},
		{name: "repeat list", call: func() error { _, err := list.List(context.Background(), cli.Flags{}); return err }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			if !errors.Is(err, errNilCatalogManager) || !strings.Contains(err.Error(), "create catalog manager: catalog manager factory returned nil manager") {
				t.Fatalf("operation error = %v, want controlled typed-nil error", err)
			}
		})
	}
	if managerFactoryCalls != 1 {
		t.Fatalf("manager factory calls = %d, want 1", managerFactoryCalls)
	}
	if typedNilCatalogManagerSnapshotCalls != 0 {
		t.Fatalf("typed-nil delegate calls = %d, want 0", typedNilCatalogManagerSnapshotCalls)
	}
}

func TestLazyCatalogManagerCachesOrdinaryNilAndFactoryFailures(t *testing.T) {
	factoryCause := errors.New("catalog provider configuration failed")
	for _, test := range []struct {
		name    string
		factory func() (lifecycle.CatalogManager, error)
		want    error
		message string
	}{
		{name: "ordinary nil", factory: func() (lifecycle.CatalogManager, error) { return nil, nil }, want: errNilCatalogManager, message: "create catalog manager: catalog manager factory returned nil manager"},
		{name: "factory error", factory: func() (lifecycle.CatalogManager, error) { return nil, factoryCause }, want: factoryCause, message: "create catalog manager: catalog provider configuration failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			factoryCalls := 0
			manager := &lazyCatalogManager{newManager: func() (lifecycle.CatalogManager, error) {
				factoryCalls++
				return test.factory()
			}}
			for attempt := 0; attempt < 2; attempt++ {
				_, _, err := manager.Snapshot(context.Background())
				if !errors.Is(err, test.want) || err.Error() != test.message {
					t.Fatalf("Snapshot attempt %d error = %v, want %q with cause %v", attempt, err, test.message, test.want)
				}
			}
			if factoryCalls != 1 {
				t.Fatalf("factory calls = %d, want 1", factoryCalls)
			}
		})
	}
}

func mainTestCatalogSnapshot(t *testing.T) catalog.Snapshot {
	t.Helper()
	entry := domain.CatalogEntry{
		Ref:         domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
		Description: "test power", Message: "test message", ContentType: "application/gzip",
		ContentSize: 1, ContentMD5: "00000000000000000000000000000000", PublishedAt: "2026-08-25T00:00:00Z", Tags: []string{},
	}
	data, err := catalog.Encode([]domain.CatalogEntry{entry})
	if err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	snapshot, err := catalog.Decode(data)
	if err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	return snapshot
}

func TestInstallCompositionSharesSelectedClientWithCacheAndPluginResource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	client := &recordingCloudClient{}
	var cacheSource cache.Source
	var pluginSource pluginresource.Downloader

	service, err := newInstallLifecycleWithCloudFactories(
		func() (cloud.Client, error) { return client, nil },
		func(source cache.Source) (cache.PowerCache, error) {
			cacheSource = source
			return cache.NewFilesystem(source)
		},
		func(source pluginresource.Downloader) (pluginresource.Manager, error) {
			pluginSource = source
			return newPluginResources(source)
		},
	)
	if err != nil {
		t.Fatalf("compose install lifecycle: %v", err)
	}
	if service == nil {
		t.Fatal("composed install lifecycle = nil")
	}
	if cacheSource != client || pluginSource != client {
		t.Fatalf("downstream sources = (%T %p, %T %p), want selected Client %p twice", cacheSource, cacheSource, pluginSource, pluginSource, client)
	}
}

type recordingCloudClient struct{}

func (*recordingCloudClient) CatalogVersion(context.Context) (domain.CatalogVersion, error) {
	return domain.CatalogVersion{}, nil
}
func (*recordingCloudClient) DownloadCatalog(context.Context, string) error { return nil }
func (*recordingCloudClient) DownloadSkill(context.Context, domain.CatalogEntry, string) error {
	return nil
}
func (*recordingCloudClient) DownloadPlugin(context.Context, domain.CatalogEntry, string) error {
	return nil
}
func (*recordingCloudClient) GetMCP(context.Context, domain.CatalogEntry) (domain.MCPServer, error) {
	return domain.MCPServer{}, nil
}
func (*recordingCloudClient) DownloadAgent(context.Context, domain.CatalogEntry, string) error {
	return nil
}
func (*recordingCloudClient) Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionResult, error) {
	return domain.SubmissionResult{}, nil
}
func (*recordingCloudClient) GetSubmission(context.Context, string) (domain.Submission, error) {
	return domain.Submission{}, nil
}
func (*recordingCloudClient) ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error) {
	return domain.SubmissionList{}, nil
}

func pluginRefForMainTest() domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
}

func TestNewSearchLifecycleSearchesBundledResources(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure APM home: %v", err)
	}
	t.Setenv("APM_HOME", home)
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatalf("secure cache home: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	service, err := newSearchLifecycle()
	if err != nil {
		t.Fatalf("newSearchLifecycle() error = %v", err)
	}
	got, err := service.Search(context.Background(), cli.Flags{Keyword: "summary"})
	if err != nil {
		t.Fatalf("Search(summary) error = %v", err)
	}
	want := cli.SearchResult{Powers: []cli.ListedPower{{
		Power:       domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.1.0"},
		Description: "Use when preparing a user-facing summary or completion note.",
		Tags:        []string{},
	}}}
	if len(got.Powers) != len(want.Powers) || len(got.Powers) != 1 || !reflect.DeepEqual(got.Powers[0], want.Powers[0]) {
		t.Fatalf("Search(summary) = %#v, want %#v", got, want)
	}
}

func TestNewSearchLifecycleRequiresCatalogLockPath(t *testing.T) {
	lockPathBlocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(lockPathBlocker, nil, 0o600); err != nil {
		t.Fatalf("create lock path blocker: %v", err)
	}
	t.Setenv("APM_HOME", lockPathBlocker)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	service, err := newSearchLifecycle()
	if err != nil {
		t.Fatalf("newSearchLifecycle() error = %v", err)
	}
	_, err = service.Search(context.Background(), cli.Flags{Keyword: "summary"})
	if err == nil || !strings.Contains(err.Error(), "acquire catalog synchronization lock") {
		t.Fatalf("Search(summary) error = %v, want catalog lock failure", err)
	}
}

func TestDefaultLifecycleFactoriesConstructWithMCPRecoveryResolver(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	clearCloudEnvironment(t)

	factories := []func() (any, error){
		func() (any, error) { return newSearchLifecycle() },
		func() (any, error) { return newListLifecycle() },
		func() (any, error) { return newInstallLifecycle() },
		func() (any, error) { return newUninstallLifecycle() },
		func() (any, error) { return newFixLifecycle() },
	}
	for index, factory := range factories {
		got, err := factory()
		if err != nil || got == nil {
			t.Fatalf("factory %d = %v, %v", index, got, err)
		}
	}
}

func TestAnonymousRemoteSearchListAndInstallLifecyclesConstruct(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APM_HOME", root)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	clearCloudEnvironment(t)
	// 未选中的 Local 根故意无效，证明三个匿名 lifecycle 都惰性选择无 Key 的 Remote Client。
	t.Setenv("APM_CLOUD_LOCAL_ROOT", "relative/resources")

	factories := []struct {
		name string
		open func() (any, error)
	}{
		{name: "search", open: func() (any, error) { return newSearchLifecycle() }},
		{name: "list", open: func() (any, error) { return newListLifecycle() }},
		{name: "install", open: func() (any, error) { return newInstallLifecycle() }},
	}
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			got, err := factory.open()
			if err != nil || got == nil {
				t.Fatalf("factory = %T, %v, want non-nil anonymous Remote lifecycle", got, err)
			}
		})
	}
}

func TestNewValidateLifecycleConstructsOffline(t *testing.T) {
	clearCloudEnvironment(t)
	service, err := newValidateLifecycle()
	if err != nil {
		t.Fatalf("newValidateLifecycle() error = %v", err)
	}
	if service == nil {
		t.Fatal("newValidateLifecycle() = nil, want service")
	}
}

func TestNewFixLifecycleSucceedsWithoutActiveJournal(t *testing.T) {
	t.Setenv("APM_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	t.Setenv("APM_CLOUD_PROVIDER", "unknown")
	t.Setenv("APC_BASE_URL", "://invalid")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", "relative/resources")

	service, err := newFixLifecycle()
	if err != nil {
		t.Fatalf("newFixLifecycle() error = %v", err)
	}
	if err := service.Fix(context.Background()); err != nil {
		t.Fatalf("Fix() error = %v", err)
	}
}

func TestNewListLifecycleListsBundledCloud(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure APM home: %v", err)
	}
	t.Setenv("APM_HOME", home)
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatalf("secure cache home: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	service, err := newListLifecycle()
	if err != nil {
		t.Fatalf("newListLifecycle() error = %v", err)
	}
	result, err := service.List(context.Background(), cli.Flags{Type: "skill"})
	if err != nil {
		t.Fatalf("List(skill) error = %v", err)
	}
	want := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.1.0"}
	stale := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.0.1"}
	found := false
	for _, item := range result.Powers {
		if item.Agent == "" && item.Power == want {
			found = true
		}
		if item.Agent == "" && item.Power == stale {
			t.Fatalf("List(skill) = %#v, want no stale %#v", result, stale)
		}
	}
	if !found {
		t.Fatalf("List(skill) = %#v, want bundled %#v", result, want)
	}
}

func TestNewListLifecycleListsBundledCloudByName(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure APM home: %v", err)
	}
	t.Setenv("APM_HOME", home)
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatalf("secure cache home: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	service, err := newListLifecycle()
	if err != nil {
		t.Fatalf("newListLifecycle() error = %v", err)
	}
	result, err := service.List(context.Background(), cli.Flags{Type: "skill", Name: "foo-before-summary"})
	if err != nil {
		t.Fatalf("List(skill foo-before-summary) error = %v", err)
	}
	want := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.1.0"}
	if len(result.Powers) != 1 || result.Powers[0].Agent != "" || result.Powers[0].Power != want {
		t.Fatalf("List(skill foo-before-summary) = %#v, want only %#v", result, want)
	}
}

func TestNewListLifecycleListsBundledCloudAllVersionsByName(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure APM home: %v", err)
	}
	t.Setenv("APM_HOME", home)
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatalf("secure cache home: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	service, err := newListLifecycle()
	if err != nil {
		t.Fatalf("newListLifecycle() error = %v", err)
	}
	result, err := service.List(context.Background(), cli.Flags{Type: "skill", Name: "foo-before-summary", All: true})
	if err != nil {
		t.Fatalf("List(skill foo-before-summary --all) error = %v", err)
	}
	if len(result.Powers) != 2 {
		t.Fatalf("List(skill foo-before-summary --all) returned %d powers, want 2: %#v", len(result.Powers), result)
	}
	wantVersions := []string{"v0.1.0", "v0.0.1"}
	for index, wantVersion := range wantVersions {
		if got := result.Powers[index].Power.Version; got != wantVersion {
			t.Fatalf("List(... --all)[%d] version = %q, want %q; result=%#v", index, got, wantVersion, result)
		}
	}
}

func TestBundledCLIRejectsListAllWithoutName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)
	var stdout, stderr bytes.Buffer
	if err := newBundledCLIEntry(t, &stdout, &stderr).Execute([]string{"list", "--all"}); err == nil || !strings.Contains(err.Error(), "--all requires name") {
		t.Fatalf("Execute(list --all) error = %v, want --all requires name", err)
	}
}

func TestNewShowLifecycleListsBundledCloudLatestAndExactVersion(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure APM home: %v", err)
	}
	t.Setenv("APM_HOME", home)
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatalf("secure cache home: %v", err)
	}
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	service, err := newCatalogLifecycleComposition().newShowLifecycle()
	if err != nil {
		t.Fatalf("newShowLifecycle() error = %v", err)
	}
	latest, err := service.Show(context.Background(), cli.Flags{Name: "foo-before-summary", Type: "skill"})
	if err != nil {
		t.Fatalf("Show(latest) error = %v", err)
	}
	if latest.Power.Power.Version != "v0.1.0" || latest.Power.Description != "Use when preparing a user-facing summary or completion note." {
		t.Fatalf("Show(latest) = %#v, want v0.1.0 bundled metadata", latest)
	}
	exact, err := service.Show(context.Background(), cli.Flags{Name: "foo-before-summary", Type: "skill", Version: "v0.0.1"})
	if err != nil {
		t.Fatalf("Show(exact) error = %v", err)
	}
	if exact.Power.Power.Version != "v0.0.1" || exact.Power.Description == latest.Power.Description {
		t.Fatalf("Show(exact) = %#v, want v0.0.1 metadata distinct from latest", exact)
	}
}

func TestBundledCLIShowLatestAndExactVersion(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("secure APM home: %v", err)
	}
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatalf("secure cache home: %v", err)
	}
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	var stdout, stderr bytes.Buffer
	entry := newBundledCLIEntry(t, &stdout, &stderr)
	if err := entry.Execute([]string{"show", "foo-before-summary", "--type=skill"}); err != nil {
		t.Fatalf("Execute(show latest) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "type: skill\nname: foo-before-summary\nversion: v0.1.0\n") || !strings.Contains(stdout.String(), "description: Use when preparing a user-facing summary or completion note.") {
		t.Fatalf("show latest output = %q", stdout.String())
	}
	stdout.Reset()
	if err := entry.Execute([]string{"show", "foo-before-summary", "--type=skill", "--version=v0.0.1", "--json"}); err != nil {
		t.Fatalf("Execute(show exact json) error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"version": "v0.0.1"`) || !strings.Contains(stdout.String(), `"description": "Use when preparing any user-facing summary`) {
		t.Fatalf("show exact output = %q", stdout.String())
	}
}

func TestNewListLifecycleListsInstalledManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	clearCloudEnvironment(t)
	// 已安装列表不依赖目录；故意损坏 Local 配置不能阻断该离线路径。
	t.Setenv("APM_CLOUD_PROVIDER", "local")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", "relative/resources")

	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"}
	if err := config.NewJSONRepository().Set(domain.AgentCodex, domain.InstalledPower{Ref: ref}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	service, err := newListLifecycle()
	if err != nil {
		t.Fatalf("newListLifecycle() error = %v", err)
	}
	result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "codex"})
	if err != nil {
		t.Fatalf("List(installed codex) error = %v", err)
	}
	want := cli.ListedPower{Agent: domain.AgentCodex, Power: ref, Tags: []string{}}
	if len(result.Powers) != 1 || !reflect.DeepEqual(result.Powers[0], want) {
		t.Fatalf("List(installed codex) = %#v, want %#v", result, want)
	}
}

func TestNewInstallLifecycleInstallsBundledSkillAndThenReturnsUnchanged(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	service, err := newInstallLifecycle()
	if err != nil {
		t.Fatalf("newInstallLifecycle() error = %v", err)
	}
	flags := cli.Flags{Agent: "codex", Name: "foo-before-summary"}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.1.0"}

	installed, err := service.Install(context.Background(), flags)
	if err != nil {
		t.Fatalf("first Install() error = %v", err)
	}
	wantInstalled := cli.InstallResult{Items: []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   ref,
		Outcome: cli.InstallOutcomeInstalled,
	}}}
	if len(installed.Items) != len(wantInstalled.Items) || installed.Items[0] != wantInstalled.Items[0] {
		t.Fatalf("first Install() = %#v, want %#v", installed, wantInstalled)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", ref.Name, "SKILL.md")); err != nil {
		t.Fatalf("installed skill marker: %v", err)
	}
	manifested, found, err := config.NewJSONRepository().Find(domain.AgentCodex, ref.Type, ref.Name)
	if err != nil {
		t.Fatalf("find installed manifest = %v", err)
	}
	if !found || manifested.Ref != ref {
		t.Fatalf("installed manifest = (%#v, %t), want (%#v, true)", manifested, found, ref)
	}
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", ".tmp", "staging"))

	unchanged, err := service.Install(context.Background(), flags)
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	wantUnchanged := cli.InstallResult{Items: []cli.InstallItem{{
		Agent:   domain.AgentCodex,
		Power:   ref,
		Outcome: cli.InstallOutcomeUnchanged,
	}}}
	if len(unchanged.Items) != len(wantUnchanged.Items) || unchanged.Items[0] != wantUnchanged.Items[0] {
		t.Fatalf("second Install() = %#v, want %#v", unchanged, wantUnchanged)
	}
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", ".tmp", "staging"))
}

func TestNewInstallLifecycleRejectsAllWhenNoAgentsAreInstalled(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	clearCloudEnvironment(t)

	service, err := newInstallLifecycle()
	if err != nil {
		t.Fatalf("newInstallLifecycle() error = %v", err)
	}
	result, err := service.Install(context.Background(), cli.Flags{Agent: "all", Name: "foo-before-summary"})
	if err == nil {
		t.Fatal("Install(all) error = nil, want no installed Agents error")
	}
	if len(result.Items) != 0 {
		t.Fatalf("Install(all) result = %#v, want zero items", result)
	}

	assertPathAbsent(t, filepath.Join(cacheHome, "apm"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "manifests"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", ".tmp", "staging"))
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions"))
}

func TestNewUninstallLifecycleRemovesBundledSkillAndThenReportsNotInstalled(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cacheHome := t.TempDir()
	if err := os.Chmod(cacheHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	useBundledLocalCloud(t)
	changeToRepositoryRoot(t)

	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	flags := cli.Flags{Agent: "codex", Name: "foo-before-summary"}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.1.0"}
	installer, err := newInstallLifecycle()
	if err != nil {
		t.Fatalf("newInstallLifecycle() error = %v", err)
	}
	if _, err := installer.Install(context.Background(), flags); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	uninstaller, err := newUninstallLifecycle()
	if err != nil {
		t.Fatalf("newUninstallLifecycle() error = %v", err)
	}
	result, err := uninstaller.Uninstall(context.Background(), flags)
	if err != nil {
		t.Fatalf("first Uninstall() error = %v", err)
	}
	assertSingleUninstallItem(t, result, domain.AgentCodex, &ref, cli.UninstallOutcomeCompleted)
	assertPathAbsent(t, filepath.Join(home, ".codex", "skills", ref.Name))
	assertManifestEntryAbsent(t, domain.AgentCodex, ref)
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))

	var stdout, stderr bytes.Buffer
	installCommandTestLogger(t, &stdout, &stderr)
	result, err = uninstaller.Uninstall(context.Background(), flags)
	if err != nil {
		t.Fatalf("second Uninstall() error = %v", err)
	}
	assertSingleUninstallItem(t, result, domain.AgentCodex, nil, cli.UninstallOutcomeUnexecuted)
	if strings.Count(stderr.String(), "level=WARN") != 1 {
		t.Fatalf("second Uninstall() stderr = %q, want exactly one Warn", stderr.String())
	}
	for _, want := range []string{
		`msg="power is not installed"`, "operation=uninstall", "agent=codex", "power=foo-before-summary",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("second Uninstall() stderr = %q, want field %q", stderr.String(), want)
		}
	}
	if strings.Contains(stderr.String(), "level=ERROR") {
		t.Fatalf("second Uninstall() stderr = %q, want no Error", stderr.String())
	}
}

func TestNewUninstallLifecycleRemovesManifestWhenInstalledTargetIsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	clearCloudEnvironment(t)
	t.Setenv("APC_BASE_URL", "http://127.0.0.1:1")

	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "missing-target", Version: "v1.2.3"}
	if err := config.NewJSONRepository().Set(domain.AgentCodex, domain.InstalledPower{Ref: ref}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	var stdout, stderr bytes.Buffer
	installCommandTestLogger(t, &stdout, &stderr)
	uninstaller, err := newUninstallLifecycle()
	if err != nil {
		t.Fatalf("newUninstallLifecycle() error = %v", err)
	}
	result, err := uninstaller.Uninstall(context.Background(), cli.Flags{
		Agent: "codex",
		Type:  "skill",
		Name:  ref.Name,
	})
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	assertSingleUninstallItem(t, result, domain.AgentCodex, &ref, cli.UninstallOutcomeCompleted)
	if strings.Count(stderr.String(), "level=WARN") != 1 {
		t.Fatalf("Uninstall() stderr = %q, want exactly one Warn", stderr.String())
	}
	for _, want := range []string{
		`msg="installed power target is missing"`, "operation=uninstall", "agent=codex",
		"power=missing-target", "type=skill", "version=v1.2.3",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("Uninstall() stderr = %q, want field %q", stderr.String(), want)
		}
	}
	assertManifestEntryAbsent(t, domain.AgentCodex, ref)
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))
}

func TestNewUninstallLifecycleUsesFirstDuplicateManifestEntryAndRemovesAllMatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HERMES_HOME", "")
	changeToRepositoryRoot(t)
	clearCloudEnvironment(t)
	t.Setenv("APC_BASE_URL", "http://127.0.0.1:1")

	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	manifestDirectory := filepath.Join(home, ".apm", "manifests")
	if err := os.MkdirAll(manifestDirectory, 0o700); err != nil {
		t.Fatalf("create manifest directory: %v", err)
	}
	manifest := []byte(`{
  "agent": "codex",
  "skills": [
    {"name": "duplicate", "version": "v2.0.0"},
    {"name": "duplicate", "version": "v1.0.0"},
    {"name": "keep", "version": "v3.0.0"}
  ],
  "plugins": [],
  "mcps": []
}
`)
	if err := os.WriteFile(filepath.Join(manifestDirectory, "codex.json"), manifest, 0o600); err != nil {
		t.Fatalf("seed duplicate manifest: %v", err)
	}

	var stdout, stderr bytes.Buffer
	installCommandTestLogger(t, &stdout, &stderr)
	uninstaller, err := newUninstallLifecycle()
	if err != nil {
		t.Fatalf("newUninstallLifecycle() error = %v", err)
	}
	result, err := uninstaller.Uninstall(context.Background(), cli.Flags{
		Agent: "codex",
		Name:  "duplicate",
	})
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	first := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "duplicate", Version: "v2.0.0"}
	assertSingleUninstallItem(t, result, domain.AgentCodex, &first, cli.UninstallOutcomeCompleted)

	groups, err := config.NewJSONRepository().List(domain.AgentCodex)
	if err != nil {
		t.Fatalf("List() after Uninstall: %v", err)
	}
	wantSkills := []domain.InstalledPower{{Ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "keep", Version: "v3.0.0"}, Tags: []string{}}}
	if !reflect.DeepEqual(groups[domain.PowerTypeSkill], wantSkills) {
		t.Fatalf("skills after Uninstall = %#v, want %#v", groups[domain.PowerTypeSkill], wantSkills)
	}
	for _, want := range []string{
		`msg="installed power target is missing"`, "power=duplicate", "type=skill", "version=v2.0.0",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("Uninstall() stderr = %q, want field %q", stderr.String(), want)
		}
	}
	assertPathAbsent(t, filepath.Join(home, ".apm", "transactions", "active.json"))
}

func assertSingleUninstallItem(t *testing.T, result cli.UninstallResult, agent domain.AgentID, power *domain.PowerRef, outcome cli.UninstallOutcome) {
	t.Helper()
	if len(result.Items) != 1 {
		t.Fatalf("Uninstall() result = %#v, want one item", result)
	}
	item := result.Items[0]
	if item.Agent != agent || item.Outcome != outcome {
		t.Fatalf("Uninstall() item = %#v, want agent %q and outcome %q", item, agent, outcome)
	}
	if power == nil {
		if item.Power != nil {
			t.Fatalf("Uninstall() item power = %#v, want nil", item.Power)
		}
		return
	}
	if item.Power == nil || *item.Power != *power {
		t.Fatalf("Uninstall() item power = %#v, want %#v", item.Power, power)
	}
}

func assertManifestEntryAbsent(t *testing.T, agent domain.AgentID, ref domain.PowerRef) {
	t.Helper()
	manifested, found, err := config.NewJSONRepository().Find(agent, ref.Type, ref.Name)
	if err != nil {
		t.Fatalf("find manifest entry: %v", err)
	}
	if found {
		t.Fatalf("manifest entry = %#v, want absent", manifested)
	}
}

func installCommandTestLogger(t *testing.T, stdout, stderr *bytes.Buffer) {
	t.Helper()
	originalLogger := slog.Default()
	slog.SetDefault(logging.NewLogger(stdout, stderr, false))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("path %q exists or cannot be inspected: %v", path, err)
	}
}

func changeToRepositoryRoot(t *testing.T) {
	t.Helper()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(originalDirectory, "..", ".."))
	if err := os.Chdir(repositoryRoot); err != nil {
		t.Fatalf("change to repository root: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalDirectory) })
}
