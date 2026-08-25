package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/lifecycle"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/paths"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
	"apm/internal/version"
)

var errNilCatalogManager = errors.New("catalog manager factory returned nil manager")

func main() {
	catalogLifecycles := newCatalogLifecycleComposition()
	entry := cli.New(
		os.Stdout,
		os.Stderr,
		logging.SetupLogging,
		catalogLifecycles.newSearchLifecycle,
		catalogLifecycles.newListLifecycle,
		catalogLifecycles.newInstallLifecycle,
		newUninstallLifecycle,
		newFixLifecycle,
		newValidateLifecycle,
		newSubmitLifecycle,
		newSubmissionQueryLifecycle,
	)
	if err := entry.Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}

// catalogLifecycleComposition 在命令入口共享一个惰性目录 Manager，避免无目录需求的命令触发 Cloud 配置。
type catalogLifecycleComposition struct {
	manager lifecycle.CatalogManager
}

// lazyCatalogManager 首次快照请求时才构造实际 Manager，并缓存构造结果供 list/search 复用。
type lazyCatalogManager struct {
	once       sync.Once
	newManager func() (lifecycle.CatalogManager, error)
	manager    lifecycle.CatalogManager
	err        error
}

func newCatalogLifecycleComposition() *catalogLifecycleComposition {
	return newCatalogLifecycleCompositionWithManagerFactory(newDefaultCatalogManager)
}

func newCatalogLifecycleCompositionWithManagerFactory(newManager func() (lifecycle.CatalogManager, error)) *catalogLifecycleComposition {
	return &catalogLifecycleComposition{manager: &lazyCatalogManager{newManager: newManager}}
}

func (composition *catalogLifecycleComposition) newSearchLifecycle() (cli.SearchLifecycle, error) {
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	return lifecycle.NewSearch(transactions, composition.manager)
}

func (composition *catalogLifecycleComposition) newListLifecycle() (cli.ListLifecycle, error) {
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	return lifecycle.NewList(transactions, composition.manager, registry, repository)
}

func (composition *catalogLifecycleComposition) newInstallLifecycle() (cli.InstallLifecycle, error) {
	return newInstallLifecycleWithCatalogAndCloudFactories(
		composition.manager,
		newCloudClient,
		func(source cache.Source) (cache.PowerCache, error) { return cache.NewFilesystem(source) },
		newPluginResources,
	)
}

func (manager *lazyCatalogManager) Snapshot(ctx context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	delegate, err := manager.resolve()
	if err != nil {
		return catalog.Snapshot{}, catalog.SyncStatus{}, err
	}
	return delegate.Snapshot(ctx)
}

func (manager *lazyCatalogManager) SnapshotLocked(ctx context.Context) (catalog.Snapshot, catalog.SyncStatus, error) {
	delegate, err := manager.resolve()
	if err != nil {
		return catalog.Snapshot{}, catalog.SyncStatus{}, err
	}
	return delegate.SnapshotLocked(ctx)
}

func (manager *lazyCatalogManager) resolve() (lifecycle.CatalogManager, error) {
	manager.once.Do(func() {
		if manager.newManager == nil {
			manager.err = fmt.Errorf("create catalog manager: nil factory")
			return
		}
		manager.manager, manager.err = manager.newManager()
		if isNilCatalogManager(manager.manager) && manager.err == nil {
			manager.err = errNilCatalogManager
		}
	})
	if manager.err != nil {
		return nil, fmt.Errorf("create catalog manager: %w", manager.err)
	}
	return manager.manager, nil
}

// isNilCatalogManager 同时识别接口零值和包装在接口中的 typed nil，避免惰性门面委托时解引用空指针。
func isNilCatalogManager(manager lifecycle.CatalogManager) bool {
	if manager == nil {
		return true
	}
	value := reflect.ValueOf(manager)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// newDefaultCatalogManager 用同一选中 Provider、唯一目录 Repository 和全局锁构造目录同步 Manager。
func newDefaultCatalogManager() (lifecycle.CatalogManager, error) {
	cloudClient, err := newCloudClient()
	if err != nil {
		return nil, err
	}
	source, ok := cloudClient.(cloud.CatalogSource)
	if !ok {
		return nil, fmt.Errorf("selected Cloud client %T does not provide catalog snapshots", cloudClient)
	}
	catalogPath, err := paths.CatalogPath()
	if err != nil {
		return nil, fmt.Errorf("resolve catalog path: %w", err)
	}
	repository, err := catalog.NewRepository(catalogPath)
	if err != nil {
		return nil, fmt.Errorf("create catalog repository: %w", err)
	}
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create catalog lock: %w", err)
	}
	temporaryRoot, err := paths.CacheTempPath()
	if err != nil {
		return nil, fmt.Errorf("resolve catalog temporary path: %w", err)
	}
	manager, err := catalog.NewManager(source, repository, locker, temporaryRoot)
	if err != nil {
		return nil, fmt.Errorf("create catalog manager: %w", err)
	}
	return manager, nil
}

// newValidateLifecycle 创建 validate 命令所需的离线校验生命周期，无 Cloud 依赖。
func newValidateLifecycle() (cli.ValidateLifecycle, error) {
	return lifecycle.NewValidate()
}

// newSubmitLifecycle 创建 submit 命令所需的生命周期；需要 APC_API_KEY。
func newSubmitLifecycle() (cli.SubmitLifecycle, error) {
	if err := requireAPIKey(); err != nil {
		return nil, err
	}
	cloudClient, err := newCloudClient()
	if err != nil {
		return nil, err
	}
	return lifecycle.NewSubmit(cloudClient)
}

// newSubmissionQueryLifecycle 创建 submission get/list 命令所需的生命周期；需要 APC_API_KEY。
func newSubmissionQueryLifecycle() (cli.SubmissionQueryLifecycle, error) {
	if err := requireAPIKey(); err != nil {
		return nil, err
	}
	cloudClient, err := newCloudClient()
	if err != nil {
		return nil, err
	}
	return lifecycle.NewSubmissionQuery(cloudClient)
}

// newSearchLifecycle 创建 Search 命令所需的默认生命周期依赖。
func newSearchLifecycle() (cli.SearchLifecycle, error) {
	return newCatalogLifecycleComposition().newSearchLifecycle()
}

// newListLifecycle 创建 List 命令所需的默认生命周期依赖；日志仅做只读检查。
func newListLifecycle() (cli.ListLifecycle, error) {
	return newCatalogLifecycleComposition().newListLifecycle()
}

// newInstallLifecycle 创建 Install 命令所需的默认依赖，并共享清单读写 Repository。
func newInstallLifecycle() (cli.InstallLifecycle, error) {
	return newCatalogLifecycleComposition().newInstallLifecycle()
}

// cloudClientFactory 是命令级 composition 边界：每次 lifecycle 构造只能调用一次，返回的同一 Client 必须共享给全部 Cloud 消费者。
type cloudClientFactory func() (cloud.Client, error)

func newInstallLifecycleWithCloudFactory(openCloud cloudClientFactory) (cli.InstallLifecycle, error) {
	return newInstallLifecycleWithCloudFactories(
		openCloud,
		func(source cache.Source) (cache.PowerCache, error) { return cache.NewFilesystem(source) },
		newPluginResources,
	)
}

// newInstallLifecycleWithCloudFactories 只暴露无状态构造 seam，用于复验同一 Client 被注入 Cache 与 Plugin Resource。
func newInstallLifecycleWithCloudFactories(
	openCloud cloudClientFactory,
	newCache func(cache.Source) (cache.PowerCache, error),
	newPlugins func(pluginresource.Downloader) (pluginresource.Manager, error),
) (cli.InstallLifecycle, error) {
	return newInstallLifecycleWithCatalogAndCloudFactories(
		newCatalogLifecycleComposition().manager,
		openCloud,
		newCache,
		newPlugins,
	)
}

func newInstallLifecycleWithCatalogAndCloudFactories(
	catalogs lifecycle.CatalogManager,
	openCloud cloudClientFactory,
	newCache func(cache.Source) (cache.PowerCache, error),
	newPlugins func(pluginresource.Downloader) (pluginresource.Manager, error),
) (cli.InstallLifecycle, error) {
	if isNilCatalogManager(catalogs) || openCloud == nil || newCache == nil || newPlugins == nil {
		return nil, fmt.Errorf("create install lifecycle with nil composition factory: %w", cloud.ErrProviderConfig)
	}
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create default lock: %w", err)
	}
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := openCloud()
	if err != nil {
		return nil, fmt.Errorf("create install Cloud client: %w", err)
	}
	powerCache, err := newCache(cloudClient)
	if err != nil {
		return nil, fmt.Errorf("create power cache: %w", err)
	}
	pluginResources, err := newPlugins(cloudClient)
	if err != nil {
		return nil, err
	}
	return lifecycle.NewInstall(
		locker,
		transactions,
		catalogs,
		cloudClient,
		powerCache,
		pluginResources,
		registry,
		repository,
		version.NewSelector(),
	)
}

// newUninstallLifecycle 创建 Uninstall 命令所需的默认依赖，并共享清单读写 Repository。
func newUninstallLifecycle() (cli.UninstallLifecycle, error) {
	return newUninstallLifecycleWithCloudFactory(newCloudClient)
}

func newUninstallLifecycleWithCloudFactory(openCloud cloudClientFactory) (cli.UninstallLifecycle, error) {
	if openCloud == nil {
		return nil, fmt.Errorf("create uninstall lifecycle with nil Cloud factory: %w", cloud.ErrProviderConfig)
	}
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create default lock: %w", err)
	}
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := openCloud()
	if err != nil {
		return nil, fmt.Errorf("create uninstall Cloud client: %w", err)
	}
	pluginResources, err := newPluginResources(cloudClient)
	if err != nil {
		return nil, err
	}
	return lifecycle.NewUninstall(locker, transactions, cloudClient, pluginResources, registry, repository)
}

// newFixLifecycle 创建修复中断事务所需的锁、清单和事务恢复依赖。
func newFixLifecycle() (cli.FixLifecycle, error) {
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create default lock: %w", err)
	}
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	return lifecycle.NewFix(locker, transactions)
}

// newPluginResources 将 Cloud 原始包下载边界直接注入 Plugin Resource；后者独占缓存和 marketplace 管理。
func newPluginResources(source pluginresource.Downloader) (pluginresource.Manager, error) {
	if isNilPluginDownloader(source) {
		return nil, fmt.Errorf("create Plugin Resource manager with nil Cloud source: %w", pluginresource.ErrInvalidArtifact)
	}
	manager, err := pluginresource.New(source)
	if err != nil {
		return nil, fmt.Errorf("create Plugin Resource manager: %w", err)
	}
	return manager, nil
}

func isNilPluginDownloader(source pluginresource.Downloader) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
