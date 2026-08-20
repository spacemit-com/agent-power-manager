package transaction

import (
	"fmt"
	"path/filepath"

	"apm/internal/domain"
)

// validateSkillInstallRequest 校验文件型 Skill 安装事务的完整输入边界。
func validateSkillInstallRequest(request SkillInstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate install request: %w", err)
	}
	if request.Power.Type != domain.PowerTypeSkill {
		return fmt.Errorf("filesystem install power type %q: %w", request.Power.Type, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate install request: nil adapter: %w", ErrInvalidRequest)
	}
	if err := validateCanonicalStaging(request.StagingRoot, ErrInvalidRequest); err != nil {
		return fmt.Errorf("validate install request: %w", err)
	}
	if request.Previous != nil {
		if err := validatePower(*request.Previous, ErrInvalidRequest); err != nil {
			return fmt.Errorf("validate install request previous power: %w", err)
		}
		if request.Previous.Name != request.Power.Name || request.Previous.Type != request.Power.Type {
			return fmt.Errorf("validate install request previous power does not match target: %w", ErrInvalidRequest)
		}
	}
	return nil
}

// validateSkillUninstallRequest 校验文件型 Skill 卸载事务的完整输入边界。
func validateSkillUninstallRequest(request SkillUninstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate uninstall request: %w", err)
	}
	if request.Power.Type != domain.PowerTypeSkill {
		return fmt.Errorf("filesystem uninstall power type %q: %w", request.Power.Type, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate uninstall request: nil adapter: %w", ErrInvalidRequest)
	}
	return nil
}

// isFilesystemPowerType 报告该类型是否由 filesystem driver 恢复。
func isFilesystemPowerType(powerType domain.PowerType) bool {
	return powerType == domain.PowerTypeSkill || powerType == domain.PowerTypeAgent
}

// validateFilesystemJournal 校验 Skill driver 的路径、阶段与产物归属。
func validateFilesystemJournal(journal Journal, payload FilesystemJournalPayload) error {
	if !isFilesystemPowerType(journal.Power.Type) {
		return fmt.Errorf("filesystem power type %q: %w", journal.Power.Type, ErrInvalidJournal)
	}
	for name, path := range map[string]string{
		"target":          payload.TargetPath,
		"transaction dir": payload.TransactionDir,
	} {
		if err := validateAbsoluteCleanPath(name, path); err != nil {
			return err
		}
	}
	wantDir := filepath.Join(filepath.Dir(payload.TargetPath), ".apm-transactions", journal.TransactionID)
	if payload.TransactionDir != wantDir {
		return fmt.Errorf("transaction dir %q does not match %q: %w", payload.TransactionDir, wantDir, ErrInvalidJournal)
	}

	switch journal.Operation {
	case OperationInstall:
		if !validInstallPhase(journal.Phase) {
			return fmt.Errorf("install phase %q: %w", journal.Phase, ErrInvalidJournal)
		}
		if err := validateCanonicalStaging(payload.StagingRoot, ErrInvalidJournal); err != nil {
			return err
		}
		if err := validateExactArtifact("backup", payload.BackupPath, filepath.Join(wantDir, "backup")); err != nil {
			return err
		}
		if payload.QuarantinePath != "" {
			return fmt.Errorf("install quarantine path %q: %w", payload.QuarantinePath, ErrInvalidJournal)
		}
		if journal.Previous != nil {
			if err := validatePower(*journal.Previous, ErrInvalidJournal); err != nil {
				return err
			}
			if journal.Previous.Name != journal.Power.Name || journal.Previous.Type != journal.Power.Type {
				return fmt.Errorf("previous power does not match install power: %w", ErrInvalidJournal)
			}
		}
	case OperationUninstall:
		if !validUninstallPhase(journal.Phase) {
			return fmt.Errorf("uninstall phase %q: %w", journal.Phase, ErrInvalidJournal)
		}
		if journal.Previous != nil || payload.StagingRoot != "" || payload.BackupPath != "" {
			return fmt.Errorf("uninstall contains install-only fields: %w", ErrInvalidJournal)
		}
		if err := validateExactArtifact("quarantine", payload.QuarantinePath, filepath.Join(wantDir, "quarantine")); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operation %q: %w", journal.Operation, ErrInvalidJournal)
	}
	return nil
}
