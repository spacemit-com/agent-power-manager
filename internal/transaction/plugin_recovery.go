package transaction

import (
	"context"
	"fmt"

	"apm/internal/adapter"
)

// recoverPlugin 仅使用 journal 与目标 Adapter 恢复 Plugin，不连接 Cloud。
func (s *Service) recoverPlugin(ctx context.Context, journal Journal, result *RecoveryResult) error {
	if journal.Plugin.Marketplace != nil {
		return s.recoverLocalMarketplacePlugin(ctx, journal, result)
	}
	if journal.Phase == PhaseCommitting {
		result.Direction = RecoveryCommit
		return s.commitRecoveredFilesystemPlugin(journal, result)
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
	return s.recoverFilesystemPlugin(ctx, journal, fact, pluginAdapter, result)
}

func (s *Service) recoverFilesystemPlugin(ctx context.Context, journal Journal, fact adapter.PluginFact, pluginAdapter adapter.PluginAdapter, result *RecoveryResult) error {
	filesystem := journal.Plugin.Filesystem
	targetExists, err := s.pathExists(filesystem.TargetPath)
	if err != nil {
		return err
	}
	switch journal.Operation {
	case OperationInstall:
		publishExists := false
		if journal.Phase == PhaseAddingNew {
			publishExists, err = s.pathExists(filesystem.PublishPath)
			if err != nil {
				return err
			}
			// publish 尚在而 target 又出现，无法证明 target 属于本事务；不得接管或清理任一目录。
			if targetExists && publishExists {
				return fmt.Errorf("filesystem Plugin target and unpublished candidate both exist: %w", ErrIndeterminateState)
			}
		}
		if journal.Phase == PhaseAddingNew && targetExists {
			if err := s.requirePhysicalPluginTarget(filesystem.TargetPath); err != nil {
				return err
			}
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
		startedPhase := journal.Phase
		result.Direction = RecoveryRollback
		// removing-old 已单义标识旧目录恢复；先降级为 rolling-back 会让 target 的旧目录身份丢失。
		if startedPhase != PhaseRemovingOld {
			if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
				return err
			}
		}
		// removing-old 阶段的同名 target 仍可能是尚未隔离的旧版本，不得按新发布目录删除。
		if targetExists && startedPhase != PhaseRemovingOld {
			if err := s.requirePhysicalPluginTarget(filesystem.TargetPath); err != nil {
				return err
			}
			mutation, err := pluginAdapter.Remove(ctx, journal.Plugin.Target)
			if err := requireAppliedPluginMutation("disable rolled-back filesystem Plugin", mutation, err); err != nil {
				return err
			}
			if err := s.files.removeAll(filesystem.TargetPath); err != nil {
				return err
			}
		}
		if journal.Plugin.PreviousPresent && journal.Plugin.Previous != nil {
			// 同路径旧版本恢复前持久化 removing-old：此后 target 若存在，只能是未移动或已恢复的旧目录。
			// 这样恢复后的启用失败或进程中断再次 Fix 时，不会把旧目录误当作新目录删除。
			if err := s.persistRecoveryDirection(&journal, PhaseRemovingOld); err != nil {
				return err
			}
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
				if err := s.requirePhysicalPluginTarget(filesystem.PreviousQuarantinePath); err != nil {
					return err
				}
				if err := s.files.rename(filesystem.PreviousQuarantinePath, filesystem.PreviousTargetPath); err != nil {
					return err
				}
				previousExists = true
			}
			if !previousExists {
				return fmt.Errorf("originally present previous Plugin target %q is missing: %w", filesystem.PreviousTargetPath, ErrIndeterminateState)
			}
			if err := s.requirePhysicalPluginTarget(filesystem.PreviousTargetPath); err != nil {
				return err
			}
			mutation, err := pluginAdapter.Add(ctx, *journal.Plugin.Previous)
			if err := requireAppliedPluginMutation("restore previous filesystem Plugin", mutation, err); err != nil {
				return err
			}
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestorePlugin, Path: filesystem.PreviousTargetPath})
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
			if err := s.requirePhysicalPluginTarget(filesystem.TargetQuarantinePath); err != nil {
				return err
			}
			if err := s.files.rename(filesystem.TargetQuarantinePath, filesystem.TargetPath); err != nil {
				return err
			}
			targetExists = true
		}
		if journal.Plugin.TargetPresent {
			if !targetExists {
				return fmt.Errorf("originally present uninstalled Plugin target %q is missing: %w", filesystem.TargetPath, ErrIndeterminateState)
			}
			if err := s.requirePhysicalPluginTarget(filesystem.TargetPath); err != nil {
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
		if err := s.requirePhysicalPluginTarget(filesystem.TargetPath); err != nil {
			return fmt.Errorf("validate committing filesystem Plugin target: %w", err)
		}
	}
	for _, path := range []string{filesystem.PreviousQuarantinePath, filesystem.TargetQuarantinePath} {
		if path != "" {
			if err := s.validateOptionalPhysicalPluginTarget(path); err != nil {
				return fmt.Errorf("validate recovered filesystem Plugin quarantine: %w", err)
			}
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

func (s *Service) commitRecoveredPlugin(journal Journal, result *RecoveryResult) error {
	if err := s.commitRecoveredPluginManifest(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}
