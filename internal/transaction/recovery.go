package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Recover 检查并收敛任意上次未完成的集中事务。
func (s *Service) Recover(ctx context.Context) (RecoveryResult, error) {
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return RecoveryResult{}, recoveryError(fmt.Errorf("recovery dependencies are nil: %w", ErrInvalidRequest))
	}
	journal, found, err := s.journals.Inspect()
	if err != nil {
		return RecoveryResult{}, recoveryError(fmt.Errorf("inspect active transaction: %w", err))
	}
	if !found {
		return RecoveryResult{}, nil
	}
	result := RecoveryResult{
		TransactionID: journal.TransactionID,
		Operation:     journal.Operation,
		Agent:         journal.Agent,
		Power:         journal.Power,
		OriginalPhase: journal.Phase,
	}
	if err := validateJournal(journal); err != nil {
		return result, recoveryError(err)
	}
	if err := ctx.Err(); err != nil {
		return result, recoveryError(err)
	}
	state, err := inspectPathState(journal)
	if err != nil {
		return result, recoveryError(err)
	}
	direction, err := recoveryDirection(journal, state)
	if err != nil {
		return result, recoveryError(err)
	}
	result.Direction = direction
	if err := s.validateRecoveryArtifacts(journal); err != nil {
		return result, recoveryError(err)
	}

	s.debugTransaction(journal)
	switch {
	case journal.Operation == OperationInstall && direction == RecoveryRollback:
		err = s.recoverInstallRollback(ctx, journal, state, &result)
	case journal.Operation == OperationInstall && direction == RecoveryCommit:
		err = s.recoverInstallCommit(ctx, journal, state, &result)
	case journal.Operation == OperationUninstall && direction == RecoveryRollback:
		err = s.recoverUninstallRollback(ctx, journal, state, &result)
	case journal.Operation == OperationUninstall && direction == RecoveryCommit:
		err = s.recoverUninstallCommit(ctx, journal, state, &result)
	default:
		err = fmt.Errorf("operation %q direction %q: %w", journal.Operation, direction, ErrIndeterminateState)
	}
	if err != nil {
		return result, recoveryError(err)
	}
	result.Recovered = true
	return result, nil
}

// recoverInstallRollback 按固定回滚方向恢复安装事务。
func (s *Service) recoverInstallRollback(ctx context.Context, journal Journal, state PathState, result *RecoveryResult) error {
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.BackupExists {
		if state.TargetExists {
			return fmt.Errorf("install target and backup both exist during rollback: %w", ErrIndeterminateState)
		}
		if err := s.files.rename(journal.BackupPath, journal.TargetPath); err != nil {
			return fmt.Errorf("restore install backup %q to %q: %w", journal.BackupPath, journal.TargetPath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestoreTarget, Path: journal.TargetPath})
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
	if !state.TargetExists {
		return fmt.Errorf("committed install target %q is missing: %w", journal.TargetPath, ErrIndeterminateState)
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
		if err := s.files.removeAll(journal.BackupPath); err != nil {
			return fmt.Errorf("remove recovered install backup %q: %w", journal.BackupPath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveBackup, Path: journal.BackupPath})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.StagingExists {
		if err := cleanupCommittedStagingWithOps(journal, s.files); err != nil {
			return err
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveStaging, Path: journal.StagingRoot})
	}
	if err := s.cleanupRecoveredTransactionDir(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

// recoverUninstallRollback 按固定回滚方向恢复卸载事务。
func (s *Service) recoverUninstallRollback(ctx context.Context, journal Journal, state PathState, result *RecoveryResult) error {
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.QuarantineExists {
		if state.TargetExists {
			return fmt.Errorf("uninstall target and quarantine both exist during rollback: %w", ErrIndeterminateState)
		}
		if err := s.files.rename(journal.QuarantinePath, journal.TargetPath); err != nil {
			return fmt.Errorf("restore uninstall quarantine %q to %q: %w", journal.QuarantinePath, journal.TargetPath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestoreTarget, Path: journal.TargetPath})
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
		if err := s.files.removeAll(journal.QuarantinePath); err != nil {
			return fmt.Errorf("remove recovered uninstall quarantine %q: %w", journal.QuarantinePath, err)
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveQuarantine, Path: journal.QuarantinePath})
	}
	if err := s.cleanupRecoveredTransactionDir(journal, result); err != nil {
		return err
	}
	return s.deleteRecoveredJournal(journal)
}

// persistRecoveryDirection 在任何恢复变更前持久化不可逆方向。
func (s *Service) persistRecoveryDirection(journal *Journal, phase Phase) error {
	if journal.Phase == phase {
		return nil
	}
	journal.Phase = phase
	if err := s.journals.Update(*journal); err != nil {
		return fmt.Errorf("persist recovery direction %q: %w", phase, err)
	}
	return nil
}

// cleanupRecoveredTransactionDir 仅清理 journal 所属的事务产物目录。
func (s *Service) cleanupRecoveredTransactionDir(journal Journal, result *RecoveryResult) error {
	exists, err := s.pathExists(journal.TransactionDir)
	if err != nil {
		return fmt.Errorf("inspect recovered transaction directory %q: %w", journal.TransactionDir, err)
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return err
	}
	if exists {
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveTransactionDir, Path: journal.TransactionDir})
	}
	return nil
}

func (s *Service) deleteRecoveredJournal(journal Journal) error {
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return fmt.Errorf("delete recovered transaction journal: %w", err)
	}
	return nil
}

// validateRecoveryArtifacts 拒绝通过符号链接或非目录访问恢复产物。
func (s *Service) validateRecoveryArtifacts(journal Journal) error {
	root := filepath.Dir(journal.TransactionDir)
	for _, path := range []string{root, journal.TransactionDir} {
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

func recoveryError(err error) error {
	return errors.Join(ErrRecoveryFailed, err)
}
