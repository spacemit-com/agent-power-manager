package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// ShowService 实现 Cloud 目录详情查询的只读生命周期。
type ShowService struct {
	inspector transaction.Inspector
	manager   CatalogManager
}

// NewShow 创建 Show 生命周期；依赖不能为空，查询不会恢复事务或访问本地清单。
func NewShow(inspector transaction.Inspector, manager CatalogManager) (*ShowService, error) {
	switch {
	case isNilInterface(inspector):
		return nil, errors.New("create Show lifecycle with nil transaction inspector")
	case isNilInterface(manager):
		return nil, errors.New("create Show lifecycle with nil catalog manager")
	}
	return &ShowService{inspector: inspector, manager: manager}, nil
}

var _ cli.ShowLifecycle = (*ShowService)(nil)

// Show 检查事务日志后读取一次不可变目录快照，返回指定名称的最新或精确版本详情。
func (s *ShowService) Show(ctx context.Context, flags cli.Flags) (cli.ShowResult, error) {
	inspectJournal(ctx, "show", s.inspector)
	name := strings.TrimSpace(flags.Name)
	if name == "" || !domain.IsCanonicalPowerName(name) {
		return cli.ShowResult{}, fmt.Errorf("show power name %q is invalid", flags.Name)
	}
	if err := cliValidateShowVersion(flags.Version); err != nil {
		return cli.ShowResult{}, err
	}
	snapshot, status, err := s.manager.Snapshot(ctx)
	if err != nil {
		return cli.ShowResult{}, fmt.Errorf("snapshot catalog for show: %w", err)
	}
	warnStaleCatalog("show", status)
	entry, err := snapshot.Resolve(domain.PowerRef{Type: domain.PowerType(flags.Type), Name: name, Version: flags.Version})
	if err != nil {
		return cli.ShowResult{}, fmt.Errorf("show catalog power %q: %w", name, err)
	}
	return makeShowResult(entry), nil
}

func cliValidateShowVersion(version string) error {
	if version == "" {
		return nil
	}
	if _, err := domain.CanonicalPowerVersion(version); err != nil {
		return fmt.Errorf("invalid power version %q: must be vMAJOR.MINOR.PATCH", version)
	}
	return nil
}
