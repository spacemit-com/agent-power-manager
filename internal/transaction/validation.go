package transaction

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

func validateInstallRequest(request InstallRequest) error {
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

func validateUninstallRequest(request UninstallRequest) error {
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
		request.Previous.Name != request.Power.Name {
		return fmt.Errorf("invalid previous MCP reference: %w", ErrInvalidRequest)
	}
	if request.Previous.Version == request.Power.Version && (request.Server != nil || request.PreviousServer != nil) {
		return fmt.Errorf("same-version MCP update contains definitions: %w", ErrInvalidRequest)
	}
	if request.PreviousServer != nil {
		if err := request.PreviousServer.Validate(); err != nil {
			return fmt.Errorf("validate previous MCP server: %v: %w", err, ErrInvalidRequest)
		}
	}
	return nil
}

func validateMCPUninstallRequest(request MCPUninstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil || request.Power.Type != domain.PowerTypeMCP {
		return fmt.Errorf("validate MCP uninstall identity: %v: %w", err, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate MCP uninstall request: nil adapter: %w", ErrInvalidRequest)
	}
	if request.Server != nil {
		if err := request.Server.Validate(); err != nil {
			return fmt.Errorf("validate MCP uninstall server: %v: %w", err, ErrInvalidRequest)
		}
	}
	return nil
}

func validatePluginInstallRequest(request PluginInstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil || request.Power.Type != domain.PowerTypePlugin {
		return fmt.Errorf("validate Plugin install identity: %v: %w", err, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate Plugin install request: nil adapter: %w", ErrInvalidRequest)
	}
	if err := request.Definition.Validate(); err != nil {
		return fmt.Errorf("validate Plugin definition: %v: %w", err, ErrInvalidRequest)
	}
	expected, err := domain.ActualPluginName(request.Power)
	if err != nil || expected != request.Definition.ActualName {
		return fmt.Errorf("Plugin actual name %q does not match %q: %v: %w", request.Definition.ActualName, expected, err, ErrInvalidRequest)
	}
	mode := request.Adapter.DeploymentMode()
	if mode == domain.PluginDeploymentFilesystem {
		if err := validateCanonicalStaging(request.StagingRoot, ErrInvalidRequest); err != nil {
			return err
		}
	} else if mode != domain.PluginDeploymentMarketplace || request.StagingRoot != "" {
		return fmt.Errorf("Plugin deployment %q with staging %q: %w", mode, request.StagingRoot, ErrInvalidRequest)
	}
	if request.Previous != nil {
		if err := validatePower(*request.Previous, ErrInvalidRequest); err != nil ||
			request.Previous.Type != domain.PowerTypePlugin || request.Previous.Name != request.Power.Name {
			return fmt.Errorf("invalid previous Plugin reference: %w", ErrInvalidRequest)
		}
	}
	return nil
}

func validatePluginUninstallRequest(request PluginUninstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil || request.Power.Type != domain.PowerTypePlugin {
		return fmt.Errorf("validate Plugin uninstall identity: %v: %w", err, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate Plugin uninstall request: nil adapter: %w", ErrInvalidRequest)
	}
	expected, err := domain.ActualPluginName(request.Power)
	if err != nil || request.ActualName != expected {
		return fmt.Errorf("Plugin actual name %q does not match %q: %v: %w", request.ActualName, expected, err, ErrInvalidRequest)
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

func validatePluginJournal(journal Journal, payload PluginJournalPayload) error {
	if journal.Power.Type != domain.PowerTypePlugin {
		return fmt.Errorf("plugin power type %q: %w", journal.Power.Type, ErrInvalidJournal)
	}
	if !validPluginTarget(payload.Target) {
		return fmt.Errorf("invalid Plugin target %+v: %w", payload.Target, ErrInvalidJournal)
	}
	switch payload.Deployment {
	case domain.PluginDeploymentMarketplace:
		if payload.Filesystem != nil {
			return fmt.Errorf("marketplace Plugin contains filesystem payload: %w", ErrInvalidJournal)
		}
	case domain.PluginDeploymentFilesystem:
		if payload.Filesystem == nil {
			return fmt.Errorf("filesystem Plugin has no paths: %w", ErrInvalidJournal)
		}
		if err := validatePluginFilesystemJournal(journal, payload); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Plugin deployment %q: %w", payload.Deployment, ErrInvalidJournal)
	}
	switch journal.Operation {
	case OperationInstall:
		if !validMCPInstallPhase(journal.Phase) {
			return fmt.Errorf("Plugin install phase %q: %w", journal.Phase, ErrInvalidJournal)
		}
		if journal.Previous == nil {
			if payload.Previous != nil || payload.PreviousPresent || journal.Phase == PhaseRemovingOld {
				return fmt.Errorf("first Plugin install contains previous state: %w", ErrInvalidJournal)
			}
			return nil
		}
		if err := validatePower(*journal.Previous, ErrInvalidJournal); err != nil ||
			journal.Previous.Type != domain.PowerTypePlugin || journal.Previous.Name != journal.Power.Name ||
			journal.Previous.Version == journal.Power.Version || payload.Previous == nil || !validPluginTarget(*payload.Previous) {
			return fmt.Errorf("invalid previous Plugin state: %w", ErrInvalidJournal)
		}
	case OperationUninstall:
		if !validUninstallPhase(journal.Phase) || journal.Previous != nil || payload.Previous != nil || payload.PreviousPresent {
			return fmt.Errorf("invalid Plugin uninstall state: %w", ErrInvalidJournal)
		}
	default:
		return fmt.Errorf("Plugin operation %q: %w", journal.Operation, ErrInvalidJournal)
	}
	return nil
}

func validPluginTarget(target adapter.PluginTarget) bool {
	name := strings.TrimSpace(target.ActualName)
	selector := strings.TrimSpace(target.Selector)
	if name == "" || name != target.ActualName || filepath.IsAbs(name) || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return false
	}
	return selector == name || (strings.HasPrefix(selector, name+"@") && len(selector) > len(name)+1 && !strings.ContainsAny(selector, " \t\r\n"))
}

func validatePluginFilesystemJournal(journal Journal, payload PluginJournalPayload) error {
	filesystem := payload.Filesystem
	for name, path := range map[string]string{
		"plugin target": filesystem.TargetPath, "plugin transaction": filesystem.TransactionDir,
	} {
		if err := validateAbsoluteCleanPath(name, path); err != nil {
			return err
		}
	}
	if filepath.Base(filesystem.TargetPath) != payload.Target.ActualName {
		return fmt.Errorf("Plugin target path %q: %w", filesystem.TargetPath, ErrInvalidJournal)
	}
	wantDir := filepath.Join(filepath.Dir(filesystem.TargetPath), ".apm-transactions", journal.TransactionID)
	if filesystem.TransactionDir != wantDir {
		return fmt.Errorf("Plugin transaction artifacts do not match %q: %w", wantDir, ErrInvalidJournal)
	}
	switch journal.Operation {
	case OperationInstall:
		if err := validateCanonicalStaging(filesystem.StagingRoot, ErrInvalidJournal); err != nil {
			return err
		}
		if err := validateAbsoluteCleanPath("plugin source", filesystem.SourcePath); err != nil {
			return err
		}
		if err := validateAbsoluteCleanPath("plugin publish", filesystem.PublishPath); err != nil {
			return err
		}
		if filesystem.SourcePath != filepath.Join(filesystem.StagingRoot, "plugins", journal.Power.Name) || filesystem.PublishPath != filepath.Join(wantDir, "publish") {
			return fmt.Errorf("Plugin install paths do not match staging/transaction roots: %w", ErrInvalidJournal)
		}
		if journal.Previous != nil {
			if err := validateAbsoluteCleanPath("Plugin previous target", filesystem.PreviousTargetPath); err != nil {
				return err
			}
			if filesystem.PreviousQuarantinePath != filepath.Join(wantDir, "previous") || payload.Previous == nil || filepath.Base(filesystem.PreviousTargetPath) != payload.Previous.ActualName {
				return fmt.Errorf("Plugin previous artifacts invalid: %w", ErrInvalidJournal)
			}
		} else if filesystem.PreviousTargetPath != "" || filesystem.PreviousQuarantinePath != "" {
			return fmt.Errorf("Plugin contains inapplicable previous artifacts: %w", ErrInvalidJournal)
		}
		if filesystem.TargetQuarantinePath != "" {
			return fmt.Errorf("Plugin install target quarantine is set: %w", ErrInvalidJournal)
		}
	case OperationUninstall:
		if filesystem.TargetQuarantinePath != filepath.Join(wantDir, "target") || filesystem.StagingRoot != "" || filesystem.SourcePath != "" || filesystem.PublishPath != "" {
			return fmt.Errorf("Plugin uninstall artifacts invalid: %w", ErrInvalidJournal)
		}
		if filesystem.PreviousTargetPath != "" || filesystem.PreviousQuarantinePath != "" {
			return fmt.Errorf("Plugin uninstall contains previous artifacts: %w", ErrInvalidJournal)
		}
	default:
		return fmt.Errorf("Plugin filesystem operation %q: %w", journal.Operation, ErrInvalidJournal)
	}
	return nil
}

func validateFilesystemJournal(journal Journal, payload FilesystemJournalPayload) error {
	if journal.Power.Type != domain.PowerTypeSkill {
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
