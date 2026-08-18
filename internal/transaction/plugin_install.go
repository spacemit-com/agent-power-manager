package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// InstallPlugin 通过类型专属 Adapter 执行可恢复的 Plugin 安装或版本切换。
func (s *Service) InstallPlugin(ctx context.Context, request PluginInstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePluginInstallRequest(request); err != nil {
		return err
	}
	if request.Adapter.DeploymentMode() != domain.PluginDeploymentMarketplace {
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
	if request.Marketplace != nil {
		return s.installLocalMarketplacePlugin(ctx, request)
	}
	targetFact, err := request.Adapter.Inspect(ctx, request.Definition.ActualName)
	if err != nil {
		return fmt.Errorf("inspect target Plugin before install: %w", err)
	}
	if targetFact.Presence == adapter.PluginPresent {
		return fmt.Errorf("Plugin target %q already exists: %w", request.Definition.ActualName, errors.Join(adapter.ErrPluginConflict, adapter.ErrTargetConflict))
	}
	if targetFact.Presence != adapter.PluginAbsent {
		return fmt.Errorf("unexpected Plugin target presence %q: %w", targetFact.Presence, ErrIndeterminateState)
	}

	// 同版本目标缺失是修复安装，不存在需要撤销的旧实际插件。
	if request.Previous == nil || request.Previous.Version == request.Power.Version {
		request.Previous = nil
		journal, err := s.newMarketplacePluginInstallJournal(request, nil, false, PhaseAddingNew)
		if err != nil {
			return err
		}
		return s.addMarketplacePlugin(ctx, journal, request.Adapter)
	}
	previousName, err := domain.ActualPluginName(*request.Previous)
	if err != nil {
		return fmt.Errorf("derive previous Plugin actual name: %w", err)
	}
	previousFact, err := request.Adapter.Inspect(ctx, previousName)
	if err != nil {
		return fmt.Errorf("inspect previous Plugin before install: %w", err)
	}
	if previousFact.Presence != adapter.PluginPresent && previousFact.Presence != adapter.PluginAbsent {
		return fmt.Errorf("unexpected previous Plugin presence %q: %w", previousFact.Presence, ErrIndeterminateState)
	}
	previousPresent := previousFact.Presence == adapter.PluginPresent
	var previousTarget *adapter.PluginTarget
	phase := PhaseAddingNew
	if previousPresent {
		previousTarget = &adapter.PluginTarget{ActualName: previousFact.ActualName, Selector: previousFact.Selector}
		phase = PhaseRemovingOld
	} else {
		previousTarget = &adapter.PluginTarget{ActualName: previousName, Selector: previousName + "@" + request.Definition.Marketplace.Name}
	}
	journal, err := s.newMarketplacePluginInstallJournal(request, previousTarget, previousPresent, phase)
	if err != nil {
		return err
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create Plugin install journal: %w", err)
	}
	if !previousPresent {
		return s.finishMarketplacePluginAdd(ctx, journal, request.Adapter)
	}
	mutation, removeErr := request.Adapter.Remove(ctx, *journal.Plugin.Previous)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		journal.Phase = PhaseAddingNew
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(removeErr, fmt.Errorf("persist Plugin adding-new intent: %w", err))
		}
		if removeErr != nil {
			return removeErr
		}
	case adapter.PluginMutationNotApplied:
		return s.finishPluginRollbackWithoutMutation(journal, pluginMutationError("remove previous Plugin", removeErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("remove previous Plugin", removeErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("remove previous Plugin returned state %q", mutation.State), removeErr)
	}
	return s.finishMarketplacePluginAdd(ctx, journal, request.Adapter)
}

func (s *Service) addMarketplacePlugin(ctx context.Context, journal Journal, pluginAdapter adapter.PluginAdapter) error {
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create Plugin install journal: %w", err)
	}
	return s.finishMarketplacePluginAdd(ctx, journal, pluginAdapter)
}

func (s *Service) finishMarketplacePluginAdd(ctx context.Context, journal Journal, pluginAdapter adapter.PluginAdapter) error {
	mutation, addErr := pluginAdapter.Add(ctx, journal.Plugin.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		journal.Phase = PhaseCommitting
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(addErr, fmt.Errorf("persist Plugin committing direction: %w", err))
		}
		if addErr != nil {
			return addErr
		}
	case adapter.PluginMutationNotApplied:
		originalErr := pluginMutationError("add Plugin", addErr)
		if !journal.Plugin.PreviousPresent {
			return s.finishPluginRollbackWithoutMutation(journal, originalErr)
		}
		journal.Phase = PhaseRollingBack
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(originalErr, fmt.Errorf("persist Plugin rollback direction: %w", err))
		}
		restore, restoreErr := pluginAdapter.Add(ctx, *journal.Plugin.Previous)
		if err := requireAppliedPluginMutation("restore previous Plugin", restore, restoreErr); err != nil {
			return errors.Join(originalErr, err)
		}
		if err := s.journals.Delete(journal.TransactionID); err != nil {
			return errors.Join(originalErr, fmt.Errorf("delete rolled-back Plugin journal: %w", err))
		}
		return originalErr
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("add Plugin", addErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("add Plugin returned state %q", mutation.State), addErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Set(journal.Agent, journal.Power); err != nil {
		return fmt.Errorf("set Plugin manifest: %w", err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return fmt.Errorf("delete completed Plugin install journal: %w", err)
	}
	return nil
}

func (s *Service) newMarketplacePluginInstallJournal(request PluginInstallRequest, previous *adapter.PluginTarget, previousPresent bool, phase Phase) (Journal, error) {
	id, err := s.newID()
	if err != nil {
		return Journal{}, err
	}
	return Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall,
		Phase: phase, Agent: request.Agent, Power: request.Power,
		Previous: clonePowerRef(request.Previous),
		Plugin: &PluginJournalPayload{
			Deployment: domain.PluginDeploymentMarketplace,
			Target:     adapter.PluginTarget{ActualName: request.Definition.ActualName, Selector: request.Definition.ActualName + "@" + request.Definition.Marketplace.Name},
			Previous:   clonePluginTarget(previous), PreviousPresent: previousPresent,
		},
	}, nil
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
