package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"apm/internal/cli"
	"apm/internal/logging"
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

// Search 在锁内恢复共享状态，并在解锁后查询和筛选 Cloud Power。
// 生命周期只返回结构化结果，不承担 CLI 渲染。
func (s *SearchService) Search(ctx context.Context, flags cli.Flags) (cli.SearchResult, error) {
	if err := s.recoverSearch(ctx); err != nil {
		return cli.SearchResult{}, err
	}
	// Cloud 查询不访问共享状态；只有恢复锁成功释放后才允许继续，避免扩大锁区间。
	refs, err := s.cloud.List()
	if err != nil {
		return cli.SearchResult{}, fmt.Errorf("list Cloud powers for search: %w", err)
	}
	return cli.SearchResult{Powers: filterSearchPowers(flags, refs)}, nil
}

func (s *SearchService) recoverSearch(ctx context.Context) error {
	unlock, err := s.locker.Acquire()
	if err != nil {
		return fmt.Errorf("acquire search recovery lock: %w", err)
	}

	// Recover 会修改跨命令共享状态，因此获取锁后才能执行并同步记录恢复事实。
	result, recoveryErr := s.transactions.Recover(ctx)
	if recoveryErr != nil {
		releaseErr := unlock.Release()
		if releaseErr != nil {
			return errors.Join(
				fmt.Errorf("recover transaction before search: %w", recoveryErr),
				fmt.Errorf("release search recovery lock: %w", releaseErr),
			)
		}
		return fmt.Errorf("recover transaction before search: %w", recoveryErr)
	}
	if result.Recovered {
		// 成功恢复的 Warn 必须同步写入，并且仍处于全局锁保护范围内。
		slog.Warn(
			"transaction recovered",
			slog.String("operation", string(result.Operation)),
			logging.Agent(result.Agent),
			logging.Power(result.Power.Name),
			logging.Version(result.Power.Version),
			logging.Phase(string(result.OriginalPhase)),
			slog.String("direction", string(result.Direction)),
			slog.Any("actions", result.Actions),
		)
	} else {
		slog.Debug("no active transaction before search")
	}
	if err := unlock.Release(); err != nil {
		return fmt.Errorf("release search recovery lock: %w", err)
	}
	return nil
}
