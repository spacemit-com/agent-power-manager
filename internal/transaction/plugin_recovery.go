package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"

	"apm/internal/adapter"
)

// recoverPlugin 仅使用 journal 与目标 Adapter 恢复 Plugin，不连接 Cloud 或 marketplace。
func (s *Service) recoverPlugin(ctx context.Context, journal Journal, result *RecoveryResult) error {
	if journal.Phase == PhaseCommitting {
		result.Direction = RecoveryCommit
		if journal.Plugin.Filesystem != nil {
			return s.commitRecoveredFilesystemPlugin(journal, result)
		}
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
	fact, err := pluginAdapter.Inspect(ctx, journal.Plugin.Target.ActualName)
	if err != nil {
		return fmt.Errorf("inspect Plugin recovery target: %w", err)
	}
	if journal.Plugin.Filesystem != nil {
		return s.recoverFilesystemPlugin(ctx, journal, fact, pluginAdapter, result)
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

func (s *Service) recoverFilesystemPlugin(ctx context.Context, journal Journal, fact adapter.PluginFact, pluginAdapter adapter.PluginAdapter, result *RecoveryResult) error {
	filesystem := journal.Plugin.Filesystem
	targetExists, err := s.pathExists(filesystem.TargetPath)
	if err != nil {
		return err
	}
	switch journal.Operation {
	case OperationInstall:
		if journal.Phase == PhaseAddingNew && targetExists {
			mutation, err := pluginAdapter.Add(ctx, journal.Plugin.Target)
			if err := requireAppliedPluginMutation("enable recovered filesystem Plugin", mutation, err); err != nil {
				return err
			}
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionAddPlugin, Path: filesystem.TargetPath})
			result.Direction = RecoveryCommit
			if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
				return err
			}
			return s.commitRecoveredFilesystemPlugin(journal, result)
		}
		if journal.Phase != PhaseAddingNew && journal.Phase != PhaseRemovingOld && journal.Phase != PhaseRollingBack {
			return fmt.Errorf("filesystem Plugin install phase %q: %w", journal.Phase, ErrIndeterminateState)
		}
		result.Direction = RecoveryRollback
		if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
			return err
		}
		if targetExists {
			mutation, err := pluginAdapter.Remove(ctx, journal.Plugin.Target)
			if err := requireAppliedPluginMutation("disable rolled-back filesystem Plugin", mutation, err); err != nil {
				return err
			}
			if err := s.files.removeAll(filesystem.TargetPath); err != nil {
				return err
			}
		}
		if journal.Plugin.PreviousPresent && journal.Plugin.Previous != nil {
			quarantineExists, err := s.pathExists(filesystem.PreviousQuarantinePath)
			if err != nil {
				return err
			}
			previousExists, err := s.pathExists(filesystem.PreviousTargetPath)
			if err != nil {
				return err
			}
			if quarantineExists && previousExists {
				return fmt.Errorf("previous Plugin target and quarantine both exist: %w", ErrIndeterminateState)
			}
			if quarantineExists {
				if err := s.files.rename(filesystem.PreviousQuarantinePath, filesystem.PreviousTargetPath); err != nil {
					return err
				}
				mutation, err := pluginAdapter.Add(ctx, *journal.Plugin.Previous)
				if err := requireAppliedPluginMutation("restore previous filesystem Plugin", mutation, err); err != nil {
					return err
				}
				result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestorePlugin, Path: filesystem.PreviousTargetPath})
			}
		}
		artifacts, _ := filesystemPluginArtifacts(filesystem.TargetPath, journal.TransactionID)
		if err := s.cleanupPluginTransactionDir(artifacts); err != nil {
			return err
		}
		return s.deleteRecoveredJournal(journal)
	case OperationUninstall:
		quarantineExists, err := s.pathExists(filesystem.TargetQuarantinePath)
		if err != nil {
			return err
		}
		if journal.Phase == PhaseRemoving && (quarantineExists || !targetExists) {
			result.Direction = RecoveryCommit
			if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
				return err
			}
			return s.commitRecoveredFilesystemPlugin(journal, result)
		}
		result.Direction = RecoveryRollback
		if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
			return err
		}
		if quarantineExists {
			if targetExists {
				return fmt.Errorf("filesystem Plugin target and quarantine both exist: %w", ErrIndeterminateState)
			}
			if err := s.files.rename(filesystem.TargetQuarantinePath, filesystem.TargetPath); err != nil {
				return err
			}
			mutation, err := pluginAdapter.Add(ctx, journal.Plugin.Target)
			if err := requireAppliedPluginMutation("restore uninstalled filesystem Plugin", mutation, err); err != nil {
				return err
			}
		}
		artifacts, _ := filesystemPluginArtifacts(filesystem.TargetPath, journal.TransactionID)
		if err := s.cleanupPluginTransactionDir(artifacts); err != nil {
			return err
		}
		return s.deleteRecoveredJournal(journal)
	default:
		return fmt.Errorf("filesystem Plugin operation %q: %w", journal.Operation, ErrIndeterminateState)
	}
}

func (s *Service) commitRecoveredFilesystemPlugin(journal Journal, result *RecoveryResult) error {
	filesystem := journal.Plugin.Filesystem
	if journal.Operation == OperationInstall {
		info, err := s.files.lstat(filesystem.TargetPath)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("committing filesystem Plugin target %q is missing: %w", filesystem.TargetPath, ErrIndeterminateState)
		}
		if err != nil {
			return fmt.Errorf("inspect committing filesystem Plugin target %q: %w", filesystem.TargetPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("committing filesystem Plugin target %q is not a physical directory: %w", filesystem.TargetPath, ErrUnsafePath)
		}
	}
	if err := s.commitRecoveredPluginManifest(journal, result); err != nil {
		return err
	}
	for _, path := range []string{filesystem.PreviousQuarantinePath, filesystem.TargetQuarantinePath} {
		if path != "" {
			if err := s.files.removeAll(path); err != nil {
				return err
			}
		}
	}
	if journal.Operation == OperationInstall && filesystem.StagingRoot != "" {
		if err := cleanupInstallStagingWithOps(filesystem.StagingRoot, filesystem.TargetPath, s.files); err != nil {
			return err
		}
	}
	artifacts, _ := filesystemPluginArtifacts(filesystem.TargetPath, journal.TransactionID)
	if err := s.cleanupPluginTransactionDir(artifacts); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

func (s *Service) commitRecoveredPluginManifest(journal Journal, result *RecoveryResult) error {
	if journal.Operation == OperationInstall {
		if err := s.manifests.Set(journal.Agent, journal.Power); err != nil {
			return fmt.Errorf("commit recovered Plugin manifest: %w", err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionSetManifest})
		return nil
	}
	if err := s.manifests.Remove(journal.Agent, journal.Power.Type, journal.Power.Name); err != nil {
		return fmt.Errorf("commit recovered Plugin manifest: %w", err)
	}
	result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveManifest})
	return nil
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
	if err := s.commitRecoveredPluginManifest(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}
