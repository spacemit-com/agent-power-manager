// Package lifecycle 提供能力生命周期操作的占位接口和服务。
package lifecycle

import (
	"context"
	"errors"

	"apm/internal/cli"
	"apm/internal/domain"
)

// ListResult 表示能力列表查询的聚合结果。
type ListResult struct {
	CloudPowers      []domain.PowerRef
	InstalledByAgent map[domain.AgentID]domain.PowerGroups
	Failures         map[domain.AgentID]error
}

// Lifecycle 定义能力生命周期操作。
type Lifecycle interface {
	Search(context.Context, cli.Flags) (cli.SearchResult, error)
	List(cli.Flags) (ListResult, error)
	Install(cli.Flags) (domain.Result, error)
	Uninstall(cli.Flags) (domain.Result, error)
}

// Service 是具有共享状态恢复依赖的 Lifecycle 实现。
type Service struct {
	locker       Locker
	transactions TransactionManager
	cloud        CloudClient
}

// New 创建具有恢复、锁和 Cloud 依赖的生命周期服务。
func New(locker Locker, transactions TransactionManager, cloud CloudClient) (*Service, error) {
	switch {
	case locker == nil:
		return nil, errors.New("create lifecycle service with nil locker")
	case transactions == nil:
		return nil, errors.New("create lifecycle service with nil transaction manager")
	case cloud == nil:
		return nil, errors.New("create lifecycle service with nil cloud client")
	}
	return &Service{locker: locker, transactions: transactions, cloud: cloud}, nil
}

var _ Lifecycle = (*Service)(nil)
var _ cli.SearchLifecycle = (*Service)(nil)

// List 列出能力。
func (s *Service) List(cli.Flags) (ListResult, error) {
	return ListResult{}, nil
}

// Install 安装能力。
func (s *Service) Install(cli.Flags) (domain.Result, error) {
	return domain.Result{}, nil
}

// Uninstall 卸载能力。
func (s *Service) Uninstall(cli.Flags) (domain.Result, error) {
	return domain.Result{}, nil
}
