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

func validateMCPInstallRequest(request MCPInstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil || request.Power.Type != domain.PowerTypeMCP {
		return fmt.Errorf("validate MCP install identity: %v: %w", err, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate MCP install request: nil adapter: %w", ErrInvalidRequest)
	}
	if request.Server != nil {
		if err := request.Server.Validate(); err != nil {
			return fmt.Errorf("validate MCP install server: %v: %w", err, ErrInvalidRequest)
		}
	}
	if request.Previous == nil {
		if request.PreviousServer != nil {
			return fmt.Errorf("MCP previous server without previous reference: %w", ErrInvalidRequest)
		}
		return nil
	}
	if err := validatePower(*request.Previous, ErrInvalidRequest); err != nil ||
		request.Previous.Type != domain.PowerTypeMCP ||
		request.Previous.Name != request.Power.Name ||
		request.Previous.Version == request.Power.Version {
		return fmt.Errorf("invalid previous MCP reference: %w", ErrInvalidRequest)
	}
	if request.PreviousServer != nil {
		if err := request.PreviousServer.Validate(); err != nil {
			return fmt.Errorf("validate previous MCP server: %v: %w", err, ErrInvalidRequest)
		}
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
	if journal.SchemaVersion != 2 {
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
		if journal.Filesystem == nil || journal.AgentCLI != nil {
			return fmt.Errorf("filesystem driver requires only filesystem payload: %w", ErrInvalidJournal)
		}
		return validateFilesystemJournal(journal, *journal.Filesystem)
	case DriverAgentCLI:
		if journal.AgentCLI == nil || journal.Filesystem != nil {
			return fmt.Errorf("agent-cli driver requires only agent_cli payload: %w", ErrInvalidJournal)
		}
		return validateAgentCLIJournal(journal, *journal.AgentCLI)
	default:
		return fmt.Errorf("driver %q: %w", journal.Driver, ErrInvalidJournal)
	}
}

func validateFilesystemJournal(journal Journal, payload FilesystemJournalPayload) error {
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

func validateAgentCLIJournal(journal Journal, payload AgentCLIJournalPayload) error {
	if journal.Power.Type != domain.PowerTypeMCP {
		return fmt.Errorf("agent-cli power type %q: %w", journal.Power.Type, ErrInvalidJournal)
	}
	if err := payload.Server.Validate(); err != nil {
		return fmt.Errorf("agent-cli server: %v: %w", err, ErrInvalidJournal)
	}
	switch journal.Operation {
	case OperationInstall:
		if !validMCPInstallPhase(journal.Phase) {
			return fmt.Errorf("MCP install phase %q: %w", journal.Phase, ErrInvalidJournal)
		}
		if journal.Previous == nil {
			if payload.PreviousServer != nil || journal.Phase == PhaseRemovingOld {
				return fmt.Errorf("first MCP install contains previous state: %w", ErrInvalidJournal)
			}
			return nil
		}
		if err := validatePower(*journal.Previous, ErrInvalidJournal); err != nil ||
			journal.Previous.Type != domain.PowerTypeMCP ||
			journal.Previous.Name != journal.Power.Name ||
			journal.Previous.Version == journal.Power.Version || payload.PreviousServer == nil {
			return fmt.Errorf("invalid previous MCP state: %w", ErrInvalidJournal)
		}
		if err := payload.PreviousServer.Validate(); err != nil {
			return fmt.Errorf("previous MCP server: %v: %w", err, ErrInvalidJournal)
		}
	case OperationUninstall:
		if !validUninstallPhase(journal.Phase) {
			return fmt.Errorf("MCP uninstall phase %q: %w", journal.Phase, ErrInvalidJournal)
		}
		if journal.Previous != nil || payload.PreviousServer != nil {
			return fmt.Errorf("MCP uninstall contains previous state: %w", ErrInvalidJournal)
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

func validMCPInstallPhase(phase Phase) bool {
	return phase == PhasePreparing || phase == PhaseRemovingOld || phase == PhaseAddingNew || phase == PhaseRollingBack || phase == PhaseCommitting
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
