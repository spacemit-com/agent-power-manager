package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
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
	if request.Adapter.DeploymentMode() != domain.PluginDeploymentMarketplace {
		return result, fmt.Errorf("filesystem Plugin transaction is not implemented: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return result, fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return result, ErrActiveTransaction
	}
	fact, err := request.Adapter.Inspect(ctx, request.ActualName)
	if err != nil {
		return result, fmt.Errorf("inspect Plugin before uninstall: %w", err)
	}
	if fact.Presence == adapter.PluginAbsent {
		result.TargetMissing = true
		if err := s.manifests.Remove(request.Agent, domain.PowerTypePlugin, request.Power.Name); err != nil {
			return result, fmt.Errorf("remove missing Plugin manifest entry: %w", err)
		}
		return result, nil
	}
	if fact.Presence != adapter.PluginPresent {
		return result, fmt.Errorf("unexpected Plugin presence %q: %w", fact.Presence, ErrIndeterminateState)
	}
	id, err := s.newID()
	if err != nil {
		return result, err
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationUninstall,
		Phase: PhaseRemoving, Agent: request.Agent, Power: request.Power,
		Plugin: &PluginJournalPayload{Deployment: domain.PluginDeploymentMarketplace, Target: adapter.PluginTarget{ActualName: fact.ActualName, Selector: fact.Selector}, TargetPresent: true},
	}
	if err := s.journals.Create(journal); err != nil {
		return result, fmt.Errorf("create Plugin uninstall journal: %w", err)
	}
	mutation, removeErr := request.Adapter.Remove(ctx, journal.Plugin.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		journal.Phase = PhaseCommitting
		if err := s.journals.Update(journal); err != nil {
			return result, errors.Join(removeErr, fmt.Errorf("persist Plugin uninstall committing: %w", err))
		}
		if removeErr != nil {
			return result, removeErr
		}
	case adapter.PluginMutationNotApplied:
		return result, s.finishPluginRollbackWithoutMutation(journal, pluginMutationError("remove Plugin", removeErr))
	case adapter.PluginMutationUnknown:
		return result, indeterminatePluginMutation("remove Plugin", removeErr)
	default:
		return result, indeterminatePluginMutation(fmt.Sprintf("remove Plugin returned state %q", mutation.State), removeErr)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.manifests.Remove(request.Agent, domain.PowerTypePlugin, request.Power.Name); err != nil {
		return result, fmt.Errorf("remove Plugin manifest: %w", err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return result, fmt.Errorf("delete completed Plugin uninstall journal: %w", err)
	}
	return result, nil
}
