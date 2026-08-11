// Package lifecycle 提供命令专属的 Power 生命周期服务。
package lifecycle

import (
	"errors"

	"apm/internal/cli"
)

// SearchService 实现 Search 命令的恢复与 Cloud 查询生命周期。
type SearchService struct {
	locker       Locker
	transactions TransactionManager
	cloud        CloudClient
}

// NewSearch 创建具有恢复、锁和 Cloud 依赖的 Search 生命周期。
func NewSearch(locker Locker, transactions TransactionManager, cloud CloudClient) (*SearchService, error) {
	switch {
	case locker == nil:
		return nil, errors.New("create Search lifecycle with nil locker")
	case transactions == nil:
		return nil, errors.New("create Search lifecycle with nil transaction manager")
	case cloud == nil:
		return nil, errors.New("create Search lifecycle with nil Cloud client")
	}
	return &SearchService{locker: locker, transactions: transactions, cloud: cloud}, nil
}

var _ cli.SearchLifecycle = (*SearchService)(nil)
