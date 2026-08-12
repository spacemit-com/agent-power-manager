package transaction

import (
	"context"
	"errors"
	"fmt"
)

// Uninstall 执行单个 Agent 的可恢复卸载事务。
func (s *Service) Uninstall(ctx context.Context, request UninstallRequest) (UninstallResult, error) {
	result := UninstallResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateUninstallRequest(request); err != nil {
		return result, err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return result, fmt.Errorf("uninstall transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return result, fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return result, ErrActiveTransaction
	}

	target, err := request.Adapter.TargetPath(request.Power)
	if err != nil {
		return result, fmt.Errorf("resolve uninstall target: %w", err)
	}
	if err := validateSafeAbsolutePath("target", target); err != nil {
		return result, err
	}
	transactionID, err := s.newID()
	if err != nil {
		return result, err
	}
	artifacts, err := artifactPaths(target, transactionID)
	if err != nil {
		return result, err
	}
	journal := Journal{
		SchemaVersion: 2,
		TransactionID: transactionID,
		Driver:        DriverFilesystem,
		Operation:     OperationUninstall,
		Phase:         PhaseRemoving,
		Agent:         request.Agent,
		Power:         request.Power,
		Filesystem: &FilesystemJournalPayload{
			TargetPath:     target,
			TransactionDir: artifacts.dir,
			QuarantinePath: artifacts.quarantine,
		},
	}
	if err := s.journals.Create(journal); err != nil {
		return result, fmt.Errorf("create uninstall journal: %w", err)
	}
	s.debugTransaction(journal)

	if err := ctx.Err(); err != nil {
		return result, s.rollbackUninstall(journal, err)
	}
	targetExists, err := s.pathExists(target)
	if err != nil {
		return result, s.rollbackUninstall(journal, fmt.Errorf("inspect uninstall target %q: %w", target, err))
	}
	if !targetExists {
		result.TargetMissing = true
	}
	if targetExists {
		if err := ensureTransactionDirWithOps(artifacts, s.files); err != nil {
			return result, s.rollbackUninstall(journal, err)
		}
		if err := s.files.rename(target, artifacts.quarantine); err != nil {
			existsAfterFailure, inspectErr := s.pathExists(target)
			if inspectErr != nil {
				return result, errors.Join(err, fmt.Errorf("recheck uninstall target %q: %w", target, inspectErr))
			}
			if existsAfterFailure {
				return result, s.rollbackUninstall(journal, fmt.Errorf("quarantine uninstall target %q: %w", target, err))
			}
			result.TargetMissing = true
		}
	}

	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return result, fmt.Errorf("persist uninstall committing direction: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.manifests.Remove(request.Agent, request.Power.Type, request.Power.Name); err != nil {
		return result, fmt.Errorf("remove installed power manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.quarantine); err != nil {
		return result, fmt.Errorf("remove uninstall quarantine %q: %w", artifacts.quarantine, err)
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return result, err
	}
	if err := s.journals.Delete(transactionID); err != nil {
		return result, fmt.Errorf("delete completed uninstall journal: %w", err)
	}
	return result, nil
}

// rollbackUninstall 在持久化回滚方向后恢复卸载前状态。
func (s *Service) rollbackUninstall(journal Journal, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist uninstall rollback direction: %w", err))
	}
	filesystem := journal.Filesystem
	quarantineExists, err := s.pathExists(filesystem.QuarantinePath)
	if err != nil {
		return errors.Join(originalErr, fmt.Errorf("inspect uninstall quarantine %q: %w", filesystem.QuarantinePath, err))
	}
	if quarantineExists {
		targetExists, err := s.pathExists(filesystem.TargetPath)
		if err != nil {
			return errors.Join(originalErr, fmt.Errorf("inspect uninstall rollback target %q: %w", filesystem.TargetPath, err))
		}
		if targetExists {
			return errors.Join(originalErr, fmt.Errorf("rollback target and quarantine both exist: %w", ErrIndeterminateState))
		}
		if err := s.files.rename(filesystem.QuarantinePath, filesystem.TargetPath); err != nil {
			return errors.Join(originalErr, fmt.Errorf("restore uninstall quarantine %q to %q: %w", filesystem.QuarantinePath, filesystem.TargetPath, err))
		}
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return errors.Join(originalErr, err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return errors.Join(originalErr, fmt.Errorf("delete rolled-back uninstall journal: %w", err))
	}
	return originalErr
}
