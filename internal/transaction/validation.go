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
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP, domain.PowerTypeExpert:
		return nil
	default:
		return fmt.Errorf("unknown power type %q: %w", power.Type, sentinel)
	}
}

// validateJournal 校验公共 envelope 并分派到 driver validator；失败时恢复层必须保留原 journal。
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
	switch journal.Driver {
	case DriverFilesystem:
		if journal.Filesystem == nil || journal.AgentCLI != nil || journal.Plugin != nil {
			return fmt.Errorf("filesystem driver requires only filesystem payload: %w", ErrInvalidJournal)
		}
		return validateFilesystemJournal(journal, *journal.Filesystem)
	case DriverAgentCLI:
		if journal.AgentCLI == nil || journal.Filesystem != nil || journal.Plugin != nil {
			return fmt.Errorf("agent-cli driver requires only agent_cli payload: %w", ErrInvalidJournal)
		}
		return validateAgentCLIJournal(journal, *journal.AgentCLI)
	case DriverPlugin:
		if journal.Plugin == nil || journal.Filesystem != nil || journal.AgentCLI != nil {
			return fmt.Errorf("plugin driver requires only plugin payload: %w", ErrInvalidJournal)
		}
		return validatePluginJournal(journal, *journal.Plugin)
	default:
		return fmt.Errorf("driver %q: %w", journal.Driver, ErrInvalidJournal)
	}
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

func validMCPInstallPhase(phase Phase) bool {
	return phase == PhaseRemovingOld || phase == PhaseAddingNew || phase == PhaseRollingBack || phase == PhaseCommitting
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
