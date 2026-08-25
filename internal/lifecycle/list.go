package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/cli"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// ListService 实现带只读日志检查的 Cloud 与已安装清单查询。
type ListService struct {
	inspector transaction.Inspector
	manager   CatalogManager
	registry  AdapterRegistry
	config    config.Repository
}

// NewList 创建 List 生命周期；Inspector 仅用于只读检查，不得在查询中恢复事务。
func NewList(inspector transaction.Inspector, manager CatalogManager, registry AdapterRegistry, config config.Repository) (*ListService, error) {
	switch {
	case isNilInterface(inspector):
		return nil, errors.New("create List lifecycle with nil transaction inspector")
	case isNilInterface(manager):
		return nil, errors.New("create List lifecycle with nil catalog manager")
	case isNilInterface(registry):
		return nil, errors.New("create List lifecycle with nil adapter registry")
	case isNilInterface(config):
		return nil, errors.New("create List lifecycle with nil config repository")
	}
	return &ListService{
		inspector: inspector,
		manager:   manager,
		registry:  registry,
		config:    config,
	}, nil
}

var _ cli.ListLifecycle = (*ListService)(nil)

// List 先检查事务日志并告警，再查询目标清单；检查异常不阻断只读查询。
func (s *ListService) List(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
	inspectJournal(ctx, "list", s.inspector)
	if flags.Installed {
		return s.listInstalled(ctx, flags)
	}
	snapshot, status, err := s.manager.Snapshot(ctx)
	if err != nil {
		return cli.ListResult{}, fmt.Errorf("snapshot catalog powers: %w", err)
	}
	warnStaleCatalog("list", status)
	return makeCatalogListResult(flags, snapshot.List(domain.PowerType(flags.Type))), nil
}
