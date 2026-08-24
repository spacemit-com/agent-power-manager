package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

// recoverFilesystem 根据文件系统事实固定恢复方向，并收敛文件型事务。
func (s *Service) recoverFilesystem(ctx context.Context, journal Journal, result *RecoveryResult) error {
	state, err := inspectPathState(journal)
	if err != nil {
		return err
	}
	direction, err := recoveryDirection(journal, state)
	if err != nil {
		return err
	}
	result.Direction = direction
	if err := s.validateRecoveryArtifacts(journal); err != nil {
		return err
	}

	s.debugTransaction(journal)
	switch {
	case journal.Operation == OperationInstall && direction == RecoveryRollback:
		return s.recoverInstallRollback(ctx, journal, state, result)
	case journal.Operation == OperationInstall && direction == RecoveryCommit:
		return s.recoverInstallCommit(ctx, journal, state, result)
	case journal.Operation == OperationUninstall && direction == RecoveryRollback:
		return s.recoverUninstallRollback(ctx, journal, state, result)
	case journal.Operation == OperationUninstall && direction == RecoveryCommit:
		return s.recoverUninstallCommit(ctx, journal, state, result)
	default:
		return fmt.Errorf("operation %q direction %q: %w", journal.Operation, direction, ErrIndeterminateState)
	}
}

// recoverInstallRollback 按固定回滚方向恢复安装事务。
func (s *Service) recoverInstallRollback(ctx context.Context, journal Journal, state PathState, result *RecoveryResult) error {
	filesystem := journal.Filesystem
	if state.BackupExists {
		if state.TargetExists {
			return fmt.Errorf("install target and backup both exist during rollback: %w", ErrIndeterminateState)
		}
		if journal.Power.Type == domain.PowerTypeAgent {
			if journal.Previous == nil {
				return fmt.Errorf("install rollback backup %q has no previous power: %w", filesystem.BackupPath, ErrIndeterminateState)
			}
			if err := s.validateAgentRecoveryArtifact(journal, *journal.Previous, filesystem.BackupPath); err != nil {
				return err
			}
		}
	}
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.BackupExists {
		if err := s.files.rename(filesystem.BackupPath, filesystem.TargetPath); err != nil {
			return fmt.Errorf("restore install backup %q to %q: %w", filesystem.BackupPath, filesystem.TargetPath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestoreTarget, Path: filesystem.TargetPath})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.cleanupRecoveredTransactionDir(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

// recoverInstallCommit 按固定提交方向完成安装事务。
func (s *Service) recoverInstallCommit(ctx context.Context, journal Journal, state PathState, result *RecoveryResult) error {
	filesystem := journal.Filesystem
	if !state.TargetExists {
		return fmt.Errorf("committed install target %q is missing: %w", filesystem.TargetPath, ErrIndeterminateState)
	}
	if err := s.validateAgentRecoveryArtifact(journal, journal.Power, filesystem.TargetPath); err != nil {
		return err
	}
	if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Set(journal.Agent, journal.Power); err != nil {
		return fmt.Errorf("set recovered install manifest: %w", err)
	}
	result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionSetManifest})
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.BackupExists {
		if err := s.files.removeAll(filesystem.BackupPath); err != nil {
			return fmt.Errorf("remove recovered install backup %q: %w", filesystem.BackupPath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveBackup, Path: filesystem.BackupPath})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.StagingExists {
		if err := cleanupCommittedStagingWithOps(journal, s.files); err != nil {
			return err
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveStaging, Path: filesystem.StagingRoot})
	}
	if err := s.cleanupRecoveredTransactionDir(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

// recoverUninstallRollback 按固定回滚方向恢复卸载事务。
func (s *Service) recoverUninstallRollback(ctx context.Context, journal Journal, state PathState, result *RecoveryResult) error {
	filesystem := journal.Filesystem
	if state.QuarantineExists {
		if state.TargetExists {
			return fmt.Errorf("uninstall target and quarantine both exist during rollback: %w", ErrIndeterminateState)
		}
		if err := s.validateAgentRecoveryArtifact(journal, journal.Power, filesystem.QuarantinePath); err != nil {
			return err
		}
	}
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.QuarantineExists {
		if err := s.files.rename(filesystem.QuarantinePath, filesystem.TargetPath); err != nil {
			return fmt.Errorf("restore uninstall quarantine %q to %q: %w", filesystem.QuarantinePath, filesystem.TargetPath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestoreTarget, Path: filesystem.TargetPath})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.cleanupRecoveredTransactionDir(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

// recoverUninstallCommit 按固定提交方向完成卸载事务。
func (s *Service) recoverUninstallCommit(ctx context.Context, journal Journal, state PathState, result *RecoveryResult) error {
	filesystem := journal.Filesystem
	if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Remove(journal.Agent, journal.Power.Type, journal.Power.Name); err != nil {
		return fmt.Errorf("remove recovered uninstall manifest: %w", err)
	}
	result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveManifest})
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.QuarantineExists {
		if err := s.files.removeAll(filesystem.QuarantinePath); err != nil {
			return fmt.Errorf("remove recovered uninstall quarantine %q: %w", filesystem.QuarantinePath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveQuarantine, Path: filesystem.QuarantinePath})
	}
	if err := s.cleanupRecoveredTransactionDir(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

// cleanupRecoveredTransactionDir 仅清理 journal 所属的事务产物目录。
func (s *Service) cleanupRecoveredTransactionDir(journal Journal, result *RecoveryResult) error {
	transactionDir := journal.Filesystem.TransactionDir
	exists, err := s.pathExists(transactionDir)
	if err != nil {
		return fmt.Errorf("inspect recovered transaction directory %q: %w", transactionDir, err)
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return err
	}
	if exists {
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveTransactionDir, Path: transactionDir})
	}
	return nil
}

// validateRecoveryArtifacts 拒绝通过符号链接或非目录访问恢复产物。
func (s *Service) validateRecoveryArtifacts(journal Journal) error {
	transactionDir := journal.Filesystem.TransactionDir
	root := filepath.Dir(transactionDir)
	for _, path := range []string{root, transactionDir} {
		info, err := s.files.lstat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("inspect recovery artifact directory %q: %w", path, err)
		case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			return fmt.Errorf("recovery artifact directory %q is not physical: %w", path, ErrUnsafePath)
		}
	}
	return nil
}

// validateAgentRecoveryArtifact 在写入清单或改名/删除之前，校验即将成为本地已安装事实的
// Agent 恢复材料身份与 ref 完全一致；非 Agent 类型直接放行。
func (s *Service) validateAgentRecoveryArtifact(journal Journal, ref domain.PowerRef, path string) error {
	if journal.Power.Type != domain.PowerTypeAgent {
		return nil
	}
	if isNilInterface(s.agentAdapters) {
		return fmt.Errorf("resolve Agent recovery adapter: %w", ErrIndeterminateState)
	}
	resolved, err := s.agentAdapters.ResolveAgent(journal.Agent)
	if err != nil || isNilInterface(resolved) {
		return errors.Join(fmt.Errorf("resolve Agent recovery adapter for %q: %w", journal.Agent, err), ErrIndeterminateState)
	}
	if err := resolved.ValidateArtifact(ref, path); err != nil {
		return errors.Join(fmt.Errorf("validate Agent recovery artifact %+v at %q: %w", ref, path, err), ErrIndeterminateState)
	}
	return nil
}
