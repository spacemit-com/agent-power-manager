package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// SearchService 实现 Search 命令的只读日志检查与 Cloud 查询生命周期。
type SearchService struct {
	inspector transaction.Inspector
	manager   CatalogManager
}

// NewSearch 创建具有只读事务日志检查和目录快照依赖的 Search 生命周期。
func NewSearch(inspector transaction.Inspector, manager CatalogManager) (*SearchService, error) {
	switch {
	case isNilInterface(inspector):
		return nil, errors.New("create Search lifecycle with nil transaction inspector")
	case isNilInterface(manager):
		return nil, errors.New("create Search lifecycle with nil catalog manager")
	}
	return &SearchService{inspector: inspector, manager: manager}, nil
}

var _ cli.SearchLifecycle = (*SearchService)(nil)

// Search 先只读检查活动日志并告警，再查询和筛选 Cloud Power；检查失败不阻断只读查询。
func (s *SearchService) Search(ctx context.Context, flags cli.Flags) (cli.SearchResult, error) {
	inspectJournal(ctx, "search", s.inspector)
	snapshot, status, err := s.manager.Snapshot(ctx)
	if err != nil {
		return cli.SearchResult{}, fmt.Errorf("snapshot catalog for search: %w", err)
	}
	warnStaleCatalog("search", status)
	return cli.SearchResult{Powers: filterSearchPowers(flags, catalogPowers(snapshot.List(domain.PowerType(flags.Type))))}, nil
}
