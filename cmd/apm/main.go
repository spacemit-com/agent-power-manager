package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/lifecycle"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
	"apm/internal/version"
)

func main() {
	entry := cli.New(
		os.Stdout,
		os.Stderr,
		logging.SetupLogging,
		newSearchLifecycle,
		newListLifecycle,
		newInstallLifecycle,
		newUninstallLifecycle,
		newFixLifecycle,
	)
	if err := entry.Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}

// newSearchLifecycle 创建 Search 命令所需的默认生命周期依赖。
func newSearchLifecycle() (cli.SearchLifecycle, error) {
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
	}
	return lifecycle.NewSearch(transactions, cloudClient)
}

// newListLifecycle 创建 List 命令所需的默认生命周期依赖；日志仅做只读检查。
func newListLifecycle() (cli.ListLifecycle, error) {
	repository := config.NewJSONRepository()
	registry := adapter.NewRegistry()
	transactions, err := transaction.NewManager(repository, registry)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
	}
	return lifecycle.NewList(transactions, cloudClient, registry, repository)
}

// newInstallLifecycle 创建 Install 命令所需的默认依赖，并共享清单读写 Repository。
func newInstallLifecycle() (cli.InstallLifecycle, error) {
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
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
	}
	powerCache, err := cache.NewFilesystem()
	if err != nil {
		return nil, fmt.Errorf("create power cache: %w", err)
	}
	pluginResources, err := newPluginResources(cloudClient)
	if err != nil {
		return nil, err
	}
	return lifecycle.NewInstall(
		locker,
		transactions,
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
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
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

func newLocalCloudClient() (*cloud.LocalClient, error) {
	resourcesRoot, err := filepath.Abs(filepath.Join("internal", "cloud", "resources"))
	if err != nil {
		return nil, fmt.Errorf("resolve local Cloud resources: %w", err)
	}
	cloudClient, err := cloud.NewLocalClient(resourcesRoot)
	if err != nil {
		return nil, fmt.Errorf("create local Cloud client: %w", err)
	}
	return cloudClient, nil
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
