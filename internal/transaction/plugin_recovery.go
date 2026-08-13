package transaction

import (
	"context"
	"fmt"

	"apm/internal/adapter"
)

// recoverPlugin 仅使用 journal 与目标 Adapter 恢复 Plugin，不连接 Cloud 或 marketplace。
func (s *Service) recoverPlugin(ctx context.Context, journal Journal, result *RecoveryResult) error {
	if journal.Phase == PhaseCommitting {
		result.Direction = RecoveryCommit
		return s.commitRecoveredPlugin(journal, result)
	}
	if isNilInterface(s.pluginAdapters) {
		return fmt.Errorf("Plugin recovery resolver is nil: %w", ErrInvalidRequest)
	}
	pluginAdapter, err := s.pluginAdapters.ResolvePlugin(journal.Agent)
	if err != nil {
		return fmt.Errorf("resolve Plugin adapter for %q: %w", journal.Agent, err)
	}
	if isNilInterface(pluginAdapter) || pluginAdapter.DeploymentMode() != journal.Plugin.Deployment {
		return fmt.Errorf("resolved Plugin adapter deployment mismatch for %q: %w", journal.Agent, ErrInvalidRequest)
	}
	if journal.Plugin.Filesystem != nil {
		return fmt.Errorf("filesystem Plugin recovery is not implemented: %w", ErrInvalidRequest)
	}
	fact, err := pluginAdapter.Inspect(ctx, journal.Plugin.Target.ActualName)
	if err != nil {
		return fmt.Errorf("inspect Plugin recovery target: %w", err)
	}
	switch journal.Operation {
	case OperationInstall:
		return s.recoverMarketplacePluginInstall(ctx, journal, fact, pluginAdapter, result)
	case OperationUninstall:
		return s.recoverMarketplacePluginUninstall(ctx, journal, fact, pluginAdapter, result)
	default:
		return fmt.Errorf("Plugin recovery operation %q: %w", journal.Operation, ErrIndeterminateState)
	}
}

func (s *Service) recoverMarketplacePluginInstall(ctx context.Context, journal Journal, fact adapter.PluginFact, pluginAdapter adapter.PluginAdapter, result *RecoveryResult) error {
	if journal.Phase == PhaseAddingNew && fact.Presence == adapter.PluginPresent {
		result.Direction = RecoveryCommit
		if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
			return err
		}
		return s.commitRecoveredPlugin(journal, result)
	}
	if journal.Phase != PhaseAddingNew && journal.Phase != PhaseRemovingOld && journal.Phase != PhaseRollingBack {
		return fmt.Errorf("Plugin install recovery phase %q: %w", journal.Phase, ErrIndeterminateState)
	}
	result.Direction = RecoveryRollback
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if fact.Presence == adapter.PluginPresent {
		mutation, err := pluginAdapter.Remove(ctx, adapter.PluginTarget{ActualName: fact.ActualName, Selector: fact.Selector})
		if err := requireAppliedPluginMutation("remove rolled-back Plugin", mutation, err); err != nil {
			return err
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemovePlugin})
	} else if fact.Presence != adapter.PluginAbsent {
		return fmt.Errorf("unexpected Plugin recovery presence %q: %w", fact.Presence, ErrIndeterminateState)
	}
	if journal.Plugin.PreviousPresent && journal.Plugin.Previous != nil {
		previousFact, err := pluginAdapter.Inspect(ctx, journal.Plugin.Previous.ActualName)
		if err != nil {
			return fmt.Errorf("inspect previous Plugin during rollback: %w", err)
		}
		if previousFact.Presence == adapter.PluginAbsent {
			mutation, err := pluginAdapter.Add(ctx, *journal.Plugin.Previous)
			if err := requireAppliedPluginMutation("restore previous Plugin", mutation, err); err != nil {
				return err
			}
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestorePlugin})
		} else if previousFact.Presence != adapter.PluginPresent {
			return fmt.Errorf("unexpected previous Plugin presence %q: %w", previousFact.Presence, ErrIndeterminateState)
		}
	}
	return s.deleteRecoveredJournal(journal)
}

func (s *Service) recoverMarketplacePluginUninstall(ctx context.Context, journal Journal, fact adapter.PluginFact, pluginAdapter adapter.PluginAdapter, result *RecoveryResult) error {
	switch journal.Phase {
	case PhaseRemoving:
		if fact.Presence == adapter.PluginAbsent {
			result.Direction = RecoveryCommit
			if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
				return err
			}
			return s.commitRecoveredPlugin(journal, result)
		}
		if fact.Presence != adapter.PluginPresent {
			return fmt.Errorf("unexpected Plugin uninstall presence %q: %w", fact.Presence, ErrIndeterminateState)
		}
		result.Direction = RecoveryRollback
		if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
			return err
		}
		return s.deleteRecoveredJournal(journal)
	case PhaseRollingBack:
		result.Direction = RecoveryRollback
		if fact.Presence == adapter.PluginAbsent && journal.Plugin.TargetPresent {
			mutation, err := pluginAdapter.Add(ctx, journal.Plugin.Target)
			if err := requireAppliedPluginMutation("restore uninstalled Plugin", mutation, err); err != nil {
				return err
			}
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestorePlugin})
		}
		return s.deleteRecoveredJournal(journal)
	default:
		return fmt.Errorf("Plugin uninstall recovery phase %q: %w", journal.Phase, ErrIndeterminateState)
	}
}

func (s *Service) commitRecoveredPlugin(journal Journal, result *RecoveryResult) error {
	var err error
	if journal.Operation == OperationInstall {
		err = s.manifests.Set(journal.Agent, journal.Power)
		if err == nil {
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionSetManifest})
		}
	} else {
		err = s.manifests.Remove(journal.Agent, journal.Power.Type, journal.Power.Name)
		if err == nil {
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveManifest})
		}
	}
	if err != nil {
		return fmt.Errorf("commit recovered Plugin manifest: %w", err)
	}
	return s.deleteRecoveredJournal(journal)
}
