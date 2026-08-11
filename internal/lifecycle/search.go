package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/cli"
)

// Search 在锁内恢复共享状态，并在解锁后查询和筛选 Cloud Power。
// 生命周期只返回结构化结果，不承担 CLI 渲染。
func (s *SearchService) Search(ctx context.Context, flags cli.Flags) (cli.SearchResult, error) {
	if err := s.recoverSearch(ctx); err != nil {
		return cli.SearchResult{}, err
	}
	// Cloud 查询不需要锁，必须先显式释放锁，不能使用 defer 延后到此处。
	refs, err := s.cloud.List()
	if err != nil {
		return cli.SearchResult{}, fmt.Errorf("list Cloud powers for search: %w", err)
	}
	return cli.SearchResult{Powers: filterSearchPowers(flags, refs)}, nil
}
