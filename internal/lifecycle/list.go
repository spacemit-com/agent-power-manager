package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/cli"
)

// ListService 实现无锁 Cloud 查询和锁内已安装清单查询两条独立分支。
type ListService struct {
	locker       Locker
	transactions TransactionManager
	cloud        CloudClient
	registry     AdapterRegistry
	config       ConfigRepository
}

// NewList 创建 List 生命周期；默认 Cloud 分支不得调用共享状态依赖。
func NewList(locker Locker, transactions TransactionManager, cloud CloudClient, registry AdapterRegistry, config ConfigRepository) (*ListService, error) {
	switch {
	case isNilInterface(locker):
		return nil, errors.New("create List lifecycle with nil locker")
	case isNilInterface(transactions):
		return nil, errors.New("create List lifecycle with nil transaction manager")
	case isNilInterface(cloud):
		return nil, errors.New("create List lifecycle with nil Cloud client")
	case isNilInterface(registry):
		return nil, errors.New("create List lifecycle with nil adapter registry")
	case isNilInterface(config):
		return nil, errors.New("create List lifecycle with nil config repository")
	}
	return &ListService{
		locker:       locker,
		transactions: transactions,
		cloud:        cloud,
		registry:     registry,
		config:       config,
	}, nil
}

var _ cli.ListLifecycle = (*ListService)(nil)

// List 根据 Installed 选择无锁 Cloud 查询或锁内本地清单查询，并只返回结构化结果。
func (s *ListService) List(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
	if flags.Installed {
		return s.listInstalled(ctx, flags)
	}
	refs, err := s.cloud.List()
	if err != nil {
		return cli.ListResult{}, fmt.Errorf("list Cloud powers: %w", err)
	}
	return makeCloudListResult(flags, refs), nil
}
