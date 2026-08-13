package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/logging"
)

// Install 执行单个 Agent 的可恢复安装事务。
func (s *Service) Install(ctx context.Context, request InstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateInstallRequest(request); err != nil {
		return err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return fmt.Errorf("install transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return ErrActiveTransaction
	}

	target, err := request.Adapter.TargetPath(request.Power)
	if err != nil {
		return fmt.Errorf("resolve install target: %w", err)
	}
	if err := validateSafeAbsolutePath("target", target); err != nil {
		return err
	}
	targetExists, err := s.pathExists(target)
	if err != nil {
		return fmt.Errorf("inspect install target %q: %w", target, err)
	}
	if request.Previous == nil && targetExists {
		return fmt.Errorf("unmanaged install target %q: %w", target, adapter.ErrTargetConflict)
	}
	if request.Previous != nil && !targetExists {
		staleErr := fmt.Errorf("installed target %q for agent %q power %q is missing: %w", target, request.Agent, request.Power.Name, ErrStaleManifest)
		removeErr := s.manifests.Remove(request.Agent, request.Previous.Type, request.Previous.Name)
		cleanupErr := cleanupInstallStagingWithOps(request.StagingRoot, target, s.files)
		if removeErr != nil {
			return errors.Join(
				staleErr,
				fmt.Errorf("remove stale manifest entry for agent %q power %q: %w", request.Agent, request.Previous.Name, removeErr),
				cleanupErr,
			)
		}
		return errors.Join(
			fmt.Errorf("removed stale manifest entry for agent %q power %q after target disappeared: %w", request.Agent, request.Previous.Name, ErrStaleManifest),
			cleanupErr,
		)
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
		SchemaVersion: 1,
		TransactionID: transactionID,
		Driver:        DriverFilesystem,
		Operation:     OperationInstall,
		Phase:         PhasePreparing,
		Agent:         request.Agent,
		Power:         request.Power,
		Previous:      clonePowerRef(request.Previous),
		Filesystem: &FilesystemJournalPayload{
			StagingRoot:    request.StagingRoot,
			TargetPath:     target,
			TransactionDir: artifacts.dir,
			BackupPath:     artifacts.backup,
		},
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create install journal: %w", err)
	}
	s.debugTransaction(journal)

	if err := ctx.Err(); err != nil {
		return s.rollbackInstall(journal, err)
	}
	if request.Previous != nil && targetExists {
		if err := ensureTransactionDirWithOps(artifacts, s.files); err != nil {
			return s.rollbackInstall(journal, err)
		}
		if err := s.files.rename(target, artifacts.backup); err != nil {
			return s.rollbackInstall(journal, fmt.Errorf("backup install target %q to %q: %w", target, artifacts.backup, err))
		}
	}
	if exists, err := s.pathExists(target); err != nil {
		return s.rollbackInstall(journal, fmt.Errorf("recheck install target %q: %w", target, err))
	} else if exists {
		return s.rollbackInstall(journal, fmt.Errorf("install target appeared before publication %q: %w", target, adapter.ErrTargetConflict))
	}

	journal.Phase = PhasePublishing
	if err := s.journals.Update(journal); err != nil {
		return s.rollbackInstall(journal, fmt.Errorf("persist install publishing intent: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return s.rollbackInstall(journal, err)
	}
	result, installErr := request.Adapter.Install(request.Power, request.StagingRoot)
	if !result.Published {
		if installErr == nil {
			installErr = fmt.Errorf("adapter returned success before publication: %w", ErrInvalidRequest)
		}
		return s.rollbackInstall(journal, installErr)
	}

	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(installErr, fmt.Errorf("persist install committing direction: %w", err))
	}
	if installErr != nil {
		return installErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Set(request.Agent, request.Power); err != nil {
		return fmt.Errorf("set installed power manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.backup); err != nil {
		return fmt.Errorf("remove install backup %q: %w", artifacts.backup, err)
	}
	if err := cleanupCommittedStagingWithOps(journal, s.files); err != nil {
		return err
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return err
	}
	if err := s.journals.Delete(transactionID); err != nil {
		return fmt.Errorf("delete completed install journal: %w", err)
	}
	return nil
}

// rollbackInstall 在持久化回滚方向后恢复安装前状态。
func (s *Service) rollbackInstall(journal Journal, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist install rollback direction: %w", err))
	}
	filesystem := journal.Filesystem
	backupExists, err := s.pathExists(filesystem.BackupPath)
	if err != nil {
		return errors.Join(originalErr, fmt.Errorf("inspect install backup %q: %w", filesystem.BackupPath, err))
	}
	if backupExists {
		targetExists, err := s.pathExists(filesystem.TargetPath)
		if err != nil {
			return errors.Join(originalErr, fmt.Errorf("inspect rollback target %q: %w", filesystem.TargetPath, err))
		}
		if targetExists {
			return errors.Join(originalErr, fmt.Errorf("rollback target and backup both exist: %w", ErrIndeterminateState))
		}
		if err := s.files.rename(filesystem.BackupPath, filesystem.TargetPath); err != nil {
			return errors.Join(originalErr, fmt.Errorf("restore install backup %q to %q: %w", filesystem.BackupPath, filesystem.TargetPath, err))
		}
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return errors.Join(originalErr, err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return errors.Join(originalErr, fmt.Errorf("delete rolled-back install journal: %w", err))
	}
	return originalErr
}

func (s *Service) pathExists(path string) (bool, error) {
	_, err := s.files.lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func (s *Service) debugTransaction(journal Journal) {
	slog.Debug(
		"execute transaction",
		logging.Agent(journal.Agent),
		logging.Power(journal.Power.Name),
		logging.Version(journal.Power.Version),
		logging.Phase(string(journal.Phase)),
		slog.String("operation", string(journal.Operation)),
		slog.String("transaction_id", journal.TransactionID),
	)
}

func clonePowerRef(ref *domain.PowerRef) *domain.PowerRef {
	if ref == nil {
		return nil
	}
	clone := *ref
	return &clone
}
