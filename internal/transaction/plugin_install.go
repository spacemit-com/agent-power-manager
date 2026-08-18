package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// InstallPlugin 按已解析部署模式进入 Hermes filesystem 或本地 marketplace 事务；不再接受 Cloud 定义。
func (s *Service) InstallPlugin(ctx context.Context, request PluginInstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePluginInstallRequest(request); err != nil {
		return err
	}
	if request.Adapter.DeploymentMode() == domain.PluginDeploymentFilesystem {
		return s.installFilesystemPlugin(ctx, request)
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return fmt.Errorf("Plugin install dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return ErrActiveTransaction
	}
	return s.installLocalMarketplacePlugin(ctx, request)
}

func clonePluginTarget(target *adapter.PluginTarget) *adapter.PluginTarget {
	if target == nil {
		return nil
	}
	clone := *target
	return &clone
}

func (s *Service) finishPluginRollbackWithoutMutation(journal Journal, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist Plugin rollback direction: %w", err))
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return errors.Join(originalErr, fmt.Errorf("delete rolled-back Plugin journal: %w", err))
	}
	return originalErr
}

func pluginMutationError(operation string, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s was not applied", operation)
}

func indeterminatePluginMutation(operation string, err error) error {
	return errors.Join(fmt.Errorf("%s has unknown result", operation), err, ErrIndeterminateState)
}

func requireAppliedPluginMutation(operation string, mutation adapter.PluginMutationResult, err error) error {
	switch mutation.State {
	case adapter.PluginMutationApplied:
		return err
	case adapter.PluginMutationNotApplied:
		return pluginMutationError(operation, err)
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation(operation, err)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("%s returned state %q", operation, mutation.State), err)
	}
}
