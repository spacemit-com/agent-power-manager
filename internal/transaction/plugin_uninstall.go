package transaction

import (
	"context"
	"fmt"

	"apm/internal/domain"
)

// UninstallPlugin 离线使用已安装事实中的 selector 执行可恢复 Plugin 卸载。
func (s *Service) UninstallPlugin(ctx context.Context, request PluginUninstallRequest) (UninstallResult, error) {
	result := UninstallResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validatePluginUninstallRequest(request); err != nil {
		return result, err
	}
	if request.Adapter.DeploymentMode() == domain.PluginDeploymentFilesystem {
		return s.uninstallFilesystemPlugin(ctx, request)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return result, fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return result, ErrActiveTransaction
	}
	return s.uninstallLocalMarketplacePlugin(ctx, request)
}
