package transaction

import (
	"fmt"
	"path/filepath"
	"strings"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// validatePluginInstallRequest 校验 Plugin 安装定义与部署模式约束。
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

// validatePluginJournal 校验 Plugin driver 的部署载荷、阶段与前序版本。
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
