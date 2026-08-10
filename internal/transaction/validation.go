package transaction

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"apm/internal/domain"
	"apm/internal/paths"
)

func validateInstallRequest(request InstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate install request: %w", err)
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

func validateUninstallRequest(request UninstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate uninstall request: %w", err)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate uninstall request: nil adapter: %w", ErrInvalidRequest)
	}
	return nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func validateAgentAndPower(agent domain.AgentID, power domain.PowerRef) error {
	if strings.TrimSpace(string(agent)) == "" || agent == domain.AllAgents {
		return fmt.Errorf("invalid agent %q: %w", agent, ErrInvalidRequest)
	}
	return validatePower(power, ErrInvalidRequest)
}

func validatePower(power domain.PowerRef, sentinel error) error {
	if strings.TrimSpace(power.Name) == "" || strings.TrimSpace(string(power.Type)) == "" || strings.TrimSpace(power.Version) == "" {
		return fmt.Errorf("invalid power %+v: %w", power, sentinel)
	}
	switch power.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP:
		return nil
	default:
		return fmt.Errorf("unknown power type %q: %w", power.Type, sentinel)
	}
}

func validateJournal(journal Journal) error {
	if journal.SchemaVersion != 1 {
		return fmt.Errorf("schema version %d: %w", journal.SchemaVersion, ErrInvalidJournal)
	}
	if !validTransactionID(journal.TransactionID) {
		return fmt.Errorf("transaction id %q: %w", journal.TransactionID, ErrInvalidJournal)
	}
	if err := validateJournalIdentity(journal); err != nil {
		return err
	}
	for name, path := range map[string]string{
		"target":          journal.TargetPath,
		"transaction dir": journal.TransactionDir,
	} {
		if err := validateAbsoluteCleanPath(name, path); err != nil {
			return err
		}
	}
	wantDir := filepath.Join(filepath.Dir(journal.TargetPath), ".apm-transactions", journal.TransactionID)
	if journal.TransactionDir != wantDir {
		return fmt.Errorf("transaction dir %q does not match %q: %w", journal.TransactionDir, wantDir, ErrInvalidJournal)
	}

	switch journal.Operation {
	case OperationInstall:
		if !validInstallPhase(journal.Phase) {
			return fmt.Errorf("install phase %q: %w", journal.Phase, ErrInvalidJournal)
		}
		if err := validateCanonicalStaging(journal.StagingRoot, ErrInvalidJournal); err != nil {
			return err
		}
		if err := validateExactArtifact("backup", journal.BackupPath, filepath.Join(wantDir, "backup")); err != nil {
			return err
		}
		if journal.QuarantinePath != "" {
			return fmt.Errorf("install quarantine path %q: %w", journal.QuarantinePath, ErrInvalidJournal)
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
		if journal.Previous != nil || journal.StagingRoot != "" || journal.BackupPath != "" {
			return fmt.Errorf("uninstall contains install-only fields: %w", ErrInvalidJournal)
		}
		if err := validateExactArtifact("quarantine", journal.QuarantinePath, filepath.Join(wantDir, "quarantine")); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operation %q: %w", journal.Operation, ErrInvalidJournal)
	}
	return nil
}

func validateJournalIdentity(journal Journal) error {
	if strings.TrimSpace(string(journal.Agent)) == "" || journal.Agent == domain.AllAgents {
		return fmt.Errorf("journal agent %q: %w", journal.Agent, ErrInvalidJournal)
	}
	if err := validatePower(journal.Power, ErrInvalidJournal); err != nil {
		return err
	}
	return nil
}

func validInstallPhase(phase Phase) bool {
	return phase == PhasePreparing || phase == PhasePublishing || phase == PhaseRollingBack || phase == PhaseCommitting
}

func validUninstallPhase(phase Phase) bool {
	return phase == PhaseRemoving || phase == PhaseRollingBack || phase == PhaseCommitting
}

func validTransactionID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func validateCanonicalStaging(staging string, sentinel error) error {
	if err := validateAbsoluteCleanPath("staging", staging); err != nil {
		return fmt.Errorf("%v: %w", err, sentinel)
	}
	expected, err := paths.StagingPath()
	if err != nil {
		return fmt.Errorf("resolve expected staging: %w", err)
	}
	expected, err = filepath.Abs(filepath.Clean(expected))
	if err != nil {
		return fmt.Errorf("resolve expected staging: %w", err)
	}
	if staging != expected {
		return fmt.Errorf("staging %q does not match %q: %w", staging, expected, sentinel)
	}
	return nil
}

func validateAbsoluteCleanPath(name, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == filepath.VolumeName(path)+string(filepath.Separator) {
		return fmt.Errorf("%s path %q: %w", name, path, ErrInvalidJournal)
	}
	return nil
}

func validateExactArtifact(name, got, want string) error {
	if err := validateAbsoluteCleanPath(name, got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s path %q does not match %q: %w", name, got, want, ErrInvalidJournal)
	}
	return nil
}
