package main

import (
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/lifecycle"
	"apm/internal/lock"
	"apm/internal/logging"
	"apm/internal/transaction"
)

func main() {
	entry := cli.New(os.Stdout, os.Stderr, logging.SetupLogging, newSearchLifecycle, newListLifecycle)
	if err := entry.Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}

// newSearchLifecycle 创建 Search 命令所需的默认生命周期依赖。
func newSearchLifecycle() (cli.SearchLifecycle, error) {
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create default lock: %w", err)
	}
	transactions, err := transaction.NewManager(config.NewJSONRepository())
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
	}
	return lifecycle.NewSearch(locker, transactions, cloudClient)
}

// newListLifecycle 创建 List 命令所需的默认生命周期依赖，并让恢复与清单读取共享同一 Repository。
func newListLifecycle() (cli.ListLifecycle, error) {
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create default lock: %w", err)
	}
	repository := config.NewJSONRepository()
	transactions, err := transaction.NewManager(repository)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
	}
	return lifecycle.NewList(locker, transactions, cloudClient, adapter.NewRegistry(), repository)
}

func newLocalCloudClient() (cloud.Client, error) {
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
