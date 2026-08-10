package transaction

import (
	"context"
	"errors"
	"fmt"
)

// Uninstall 执行单个 Agent 的可恢复卸载事务。
func (s *Service) Uninstall(ctx context.Context, request UninstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateUninstallRequest(request); err != nil {
		return err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return fmt.Errorf("uninstall transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return ErrActiveTransaction
	}

	target, err := request.Adapter.TargetPath(request.Power)
	if err != nil {
		return fmt.Errorf("resolve uninstall target: %w", err)
	}
	if err := validateSafeAbsolutePath("target", target); err != nil {
		return err
	}
	transactionID, err := s.newID()
	if err != nil {
		return err
	}
	artifacts, err := artifactPaths(target, transactionID)
	if err != nil {
		return err
	}
	journal := Journal{
		SchemaVersion:  1,
		TransactionID:  transactionID,
		Operation:      OperationUninstall,
		Phase:          PhaseRemoving,
		Agent:          request.Agent,
		Power:          request.Power,
		TargetPath:     target,
		TransactionDir: artifacts.dir,
		QuarantinePath: artifacts.quarantine,
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create uninstall journal: %w", err)
	}
	s.debugTransaction(journal)

	if err := ctx.Err(); err != nil {
		return s.rollbackUninstall(journal, err)
	}
	targetExists, err := s.pathExists(target)
	if err != nil {
		return s.rollbackUninstall(journal, fmt.Errorf("inspect uninstall target %q: %w", target, err))
	}
	if targetExists {
		if err := ensureTransactionDirWithOps(artifacts, s.files); err != nil {
			return s.rollbackUninstall(journal, err)
		}
		if err := s.files.rename(target, artifacts.quarantine); err != nil {
			existsAfterFailure, inspectErr := s.pathExists(target)
			if inspectErr != nil {
				return errors.Join(err, fmt.Errorf("recheck uninstall target %q: %w", target, inspectErr))
			}
			if existsAfterFailure {
				return s.rollbackUninstall(journal, fmt.Errorf("quarantine uninstall target %q: %w", target, err))
			}
		}
	}

	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return fmt.Errorf("persist uninstall committing direction: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Remove(request.Agent, request.Power.Type, request.Power.Name); err != nil {
		return fmt.Errorf("remove installed power manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.quarantine); err != nil {
		return fmt.Errorf("remove uninstall quarantine %q: %w", artifacts.quarantine, err)
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return err
	}
	if err := s.journals.Delete(transactionID); err != nil {
		return fmt.Errorf("delete completed uninstall journal: %w", err)
	}
	return nil
}

func (s *Service) rollbackUninstall(journal Journal, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist uninstall rollback direction: %w", err))
	}
	quarantineExists, err := s.pathExists(journal.QuarantinePath)
	if err != nil {
		return errors.Join(originalErr, fmt.Errorf("inspect uninstall quarantine %q: %w", journal.QuarantinePath, err))
	}
	if quarantineExists {
		targetExists, err := s.pathExists(journal.TargetPath)
		if err != nil {
			return errors.Join(originalErr, fmt.Errorf("inspect uninstall rollback target %q: %w", journal.TargetPath, err))
		}
		if targetExists {
			return errors.Join(originalErr, fmt.Errorf("rollback target and quarantine both exist: %w", ErrIndeterminateState))
		}
		if err := s.files.rename(journal.QuarantinePath, journal.TargetPath); err != nil {
			return errors.Join(originalErr, fmt.Errorf("restore uninstall quarantine %q to %q: %w", journal.QuarantinePath, journal.TargetPath, err))
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
