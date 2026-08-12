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
	case isNilInterface(locker):
		return nil, errors.New("create Search lifecycle with nil locker")
	case isNilInterface(transactions):
		return nil, errors.New("create Search lifecycle with nil transaction manager")
	case isNilInterface(cloud):
		return nil, errors.New("create Search lifecycle with nil Cloud client")
	}
	return &SearchService{locker: locker, transactions: transactions, cloud: cloud}, nil
}

var _ cli.SearchLifecycle = (*SearchService)(nil)
