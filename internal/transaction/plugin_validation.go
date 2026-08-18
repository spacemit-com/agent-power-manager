package transaction

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

// validatePluginInstallRequest 校验 Plugin 安装定义与部署模式约束。
func validatePluginInstallRequest(request PluginInstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil || request.Power.Type != domain.PowerTypePlugin {
		return fmt.Errorf("validate Plugin install identity: %v: %w", err, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate Plugin install request: nil adapter: %w", ErrInvalidRequest)
	}
	mode := request.Adapter.DeploymentMode()
	if mode == domain.PluginDeploymentMarketplace && request.Agent != domain.AgentCodex && request.Agent != domain.AgentClaudeCode {
		return fmt.Errorf("marketplace Plugin install agent %q: %w", request.Agent, ErrInvalidRequest)
	}
	if mode == domain.PluginDeploymentFilesystem {
		if request.Marketplace != nil || request.PreviousTarget != nil || request.Target != (adapter.PluginTarget{}) {
			return fmt.Errorf("filesystem Plugin install contains marketplace data: %w", ErrInvalidRequest)
		}
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
	if mode == domain.PluginDeploymentMarketplace && request.Marketplace != nil {
		return validatePluginMarketplaceInstallRequest(request)
	}
	if request.PreviousTarget != nil || request.Target != (adapter.PluginTarget{}) {
		return fmt.Errorf("legacy Plugin install contains explicit targets: %w", ErrInvalidRequest)
	}
	if err := request.Definition.Validate(); err != nil {
		return fmt.Errorf("validate Plugin definition: %v: %w", err, ErrInvalidRequest)
	}
	expected, err := domain.ActualPluginName(request.Power)
	if err != nil || expected != request.Definition.ActualName {
		return fmt.Errorf("Plugin actual name %q does not match %q: %v: %w", request.Definition.ActualName, expected, err, ErrInvalidRequest)
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
	mode := request.Adapter.DeploymentMode()
	if mode == domain.PluginDeploymentMarketplace && request.Agent != domain.AgentCodex && request.Agent != domain.AgentClaudeCode {
		return fmt.Errorf("marketplace Plugin uninstall agent %q: %w", request.Agent, ErrInvalidRequest)
	}
	if mode == domain.PluginDeploymentMarketplace && request.Marketplace != nil {
		return validatePluginMarketplaceUninstallRequest(request)
	}
	if request.Marketplace != nil || !isNilInterface(request.MarketplaceAdapter) || request.Target != (adapter.PluginTarget{}) {
		return fmt.Errorf("Plugin uninstall contains inapplicable marketplace data: %w", ErrInvalidRequest)
	}
	expected, err := domain.ActualPluginName(request.Power)
	if err != nil || request.ActualName != expected {
		return fmt.Errorf("Plugin actual name %q does not match %q: %v: %w", request.ActualName, expected, err, ErrInvalidRequest)
	}
	return nil
}

func validatePluginMarketplaceInstallRequest(request PluginInstallRequest) error {
	if request.Agent != domain.AgentCodex && request.Agent != domain.AgentClaudeCode {
		return fmt.Errorf("marketplace Plugin install agent %q: %w", request.Agent, ErrInvalidRequest)
	}
	marketplace := request.Marketplace
	if marketplace == nil || isNilInterface(marketplace.Adapter) || marketplace.Adapter.DeploymentMode() != domain.PluginDeploymentMarketplace {
		return fmt.Errorf("marketplace Plugin install has no marketplace Adapter: %w", ErrInvalidRequest)
	}
	if !samePluginAdapterInstance(request.Adapter, marketplace.Adapter) {
		return fmt.Errorf("Plugin and marketplace install Adapters are not the same instance: %w", ErrInvalidRequest)
	}
	expectedName, err := domain.PluginMarketplaceName(request.Power)
	if err != nil || marketplace.Target.Name != expectedName {
		return fmt.Errorf("target marketplace %q does not match %q: %v: %w", marketplace.Target.Name, expectedName, err, ErrInvalidRequest)
	}
	expectedTarget, err := domain.ActualPluginName(request.Power)
	if err != nil || request.Target.ActualName != expectedTarget || request.Target.Selector != expectedTarget+"@"+expectedName {
		return fmt.Errorf("target Plugin %+v does not match %q: %v: %w", request.Target, expectedTarget, err, ErrInvalidRequest)
	}
	if err := validatePluginMarketplaceSource(request.Agent, request.Power, marketplace.Target.Source, ErrInvalidRequest); err != nil {
		return err
	}
	if request.Definition != (domain.PluginDefinition{}) {
		if err := request.Definition.Validate(); err != nil || request.Definition.ActualName != expectedTarget ||
			request.Definition.Marketplace.Name != expectedName || request.Definition.Marketplace.Source != marketplace.Target.Source {
			return fmt.Errorf("transitional Plugin definition does not match explicit targets: %v: %w", err, ErrInvalidRequest)
		}
	}
	if request.Previous == nil {
		if request.PreviousTarget != nil || marketplace.Previous != nil {
			return fmt.Errorf("first Plugin install contains previous targets: %w", ErrInvalidRequest)
		}
		return nil
	}
	if request.PreviousTarget == nil || marketplace.Previous == nil {
		return fmt.Errorf("Plugin upgrade is missing previous targets: %w", ErrInvalidRequest)
	}
	if marketplace.Previous.Name != expectedName || marketplace.Previous.Source == marketplace.Target.Source {
		return fmt.Errorf("previous marketplace %+v does not match target identity: %w", *marketplace.Previous, ErrInvalidRequest)
	}
	expectedPrevious, err := domain.ActualPluginName(*request.Previous)
	if err != nil || request.PreviousTarget.ActualName != expectedPrevious || request.PreviousTarget.Selector != expectedPrevious+"@"+expectedName {
		return fmt.Errorf("previous Plugin %+v does not match %q: %v: %w", request.PreviousTarget, expectedPrevious, err, ErrInvalidRequest)
	}
	return validatePluginMarketplaceSource(request.Agent, *request.Previous, marketplace.Previous.Source, ErrInvalidRequest)
}

func validatePluginMarketplaceUninstallRequest(request PluginUninstallRequest) error {
	if request.Agent != domain.AgentCodex && request.Agent != domain.AgentClaudeCode {
		return fmt.Errorf("marketplace Plugin uninstall agent %q: %w", request.Agent, ErrInvalidRequest)
	}
	if isNilInterface(request.MarketplaceAdapter) || request.MarketplaceAdapter.DeploymentMode() != domain.PluginDeploymentMarketplace {
		return fmt.Errorf("marketplace Plugin uninstall has no marketplace Adapter: %w", ErrInvalidRequest)
	}
	if !samePluginAdapterInstance(request.Adapter, request.MarketplaceAdapter) {
		return fmt.Errorf("Plugin and marketplace uninstall Adapters are not the same instance: %w", ErrInvalidRequest)
	}
	expectedName, err := domain.PluginMarketplaceName(request.Power)
	if err != nil || request.Marketplace.Name != expectedName {
		return fmt.Errorf("uninstall marketplace %q does not match %q: %v: %w", request.Marketplace.Name, expectedName, err, ErrInvalidRequest)
	}
	expectedTarget, err := domain.ActualPluginName(request.Power)
	if err != nil || request.Target.ActualName != expectedTarget || request.Target.Selector != expectedTarget+"@"+expectedName ||
		request.ActualName != "" && request.ActualName != expectedTarget {
		return fmt.Errorf("uninstall Plugin target %+v does not match %q: %v: %w", request.Target, expectedTarget, err, ErrInvalidRequest)
	}
	return validatePluginMarketplaceSource(request.Agent, request.Power, request.Marketplace.Source, ErrInvalidRequest)
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
		if payload.Marketplace == nil {
			// Task 10 删除：仅允许当前旧 producer 能生成的精确形状跨越功能切换。
			if validLegacyPluginJournal(journal, payload) {
				return nil
			}
			return fmt.Errorf("marketplace Plugin has no marketplace payload: %w", ErrInvalidJournal)
		}
		return validatePluginMarketplaceJournal(journal, payload)
	case domain.PluginDeploymentFilesystem:
		if payload.Filesystem == nil || payload.Marketplace != nil {
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

func validatePluginMarketplaceJournal(journal Journal, payload PluginJournalPayload) error {
	marketplace := *payload.Marketplace
	if journal.Agent != domain.AgentCodex && journal.Agent != domain.AgentClaudeCode {
		return fmt.Errorf("marketplace Plugin agent %q: %w", journal.Agent, ErrInvalidJournal)
	}
	expectedName, err := domain.PluginMarketplaceName(journal.Power)
	if err != nil || marketplace.Name != expectedName {
		return fmt.Errorf("marketplace name %q does not match %q: %v: %w", marketplace.Name, expectedName, err, ErrInvalidJournal)
	}
	expectedTarget, err := domain.ActualPluginName(journal.Power)
	if err != nil || payload.Target.ActualName != expectedTarget || payload.Target.Selector != expectedTarget+"@"+expectedName {
		return fmt.Errorf("Plugin target %+v does not match %q: %v: %w", payload.Target, expectedTarget, err, ErrInvalidJournal)
	}
	if err := validatePluginMarketplaceSource(journal.Agent, journal.Power, marketplace.TargetSource, ErrInvalidJournal); err != nil {
		return err
	}

	switch journal.Operation {
	case OperationInstall:
		if payload.TargetPresent {
			return fmt.Errorf("Plugin install target was already present: %w", ErrInvalidJournal)
		}
		if journal.Previous == nil {
			if payload.Previous != nil || payload.PreviousPresent || marketplace.PreviousSource != "" || marketplace.PreviousPresent ||
				!validFirstMarketplaceInstallPhase(journal.Phase) {
				return fmt.Errorf("invalid first marketplace Plugin install state: %w", ErrInvalidJournal)
			}
			return nil
		}
		if err := validatePluginPreviousReference(journal, payload, expectedName); err != nil {
			return err
		}
		if !payload.PreviousPresent || !marketplace.PreviousPresent ||
			!validMarketplaceUpgradePhase(journal.Phase) {
			return fmt.Errorf("invalid previous marketplace Plugin presence or phase: %w", ErrInvalidJournal)
		}
		return validatePluginMarketplaceSource(journal.Agent, *journal.Previous, marketplace.PreviousSource, ErrInvalidJournal)
	case OperationUninstall:
		if journal.Previous != nil || payload.Previous != nil || payload.PreviousPresent || !payload.TargetPresent ||
			marketplace.PreviousSource != "" || marketplace.PreviousPresent || !validMarketplaceUninstallPhase(journal.Phase) {
			return fmt.Errorf("invalid marketplace Plugin uninstall state: %w", ErrInvalidJournal)
		}
		return nil
	default:
		return fmt.Errorf("Plugin operation %q: %w", journal.Operation, ErrInvalidJournal)
	}
}

func validatePluginPreviousReference(journal Journal, payload PluginJournalPayload, marketplaceName string) error {
	if journal.Previous == nil || payload.Previous == nil {
		return fmt.Errorf("missing previous Plugin state: %w", ErrInvalidJournal)
	}
	previous := *journal.Previous
	if err := validatePower(previous, ErrInvalidJournal); err != nil || previous.Type != domain.PowerTypePlugin ||
		previous.Name != journal.Power.Name || previous.Version == journal.Power.Version {
		return fmt.Errorf("invalid previous Plugin reference: %w", ErrInvalidJournal)
	}
	expected, err := domain.ActualPluginName(previous)
	if err != nil || payload.Previous.ActualName != expected || payload.Previous.Selector != expected+"@"+marketplaceName {
		return fmt.Errorf("previous Plugin target %+v does not match %q: %v: %w", payload.Previous, expected, err, ErrInvalidJournal)
	}
	return nil
}

func validatePluginMarketplaceSource(agent domain.AgentID, power domain.PowerRef, source string, sentinel error) error {
	if source == "" || !filepath.IsAbs(source) || filepath.Clean(source) != source || source == filepath.VolumeName(source)+string(filepath.Separator) {
		return fmt.Errorf("Plugin marketplace source path %q: %w", source, sentinel)
	}
	cacheRoot, err := paths.CachePath()
	if err != nil {
		return fmt.Errorf("resolve Plugin cache root: %v: %w", err, sentinel)
	}
	cacheRoot, err = filepath.Abs(filepath.Clean(cacheRoot))
	if err != nil {
		return fmt.Errorf("resolve absolute Plugin cache root: %v: %w", err, sentinel)
	}
	expected := filepath.Join(cacheRoot, "plugins", string(agent), power.Name, power.Version)
	if source != expected {
		return fmt.Errorf("Plugin marketplace source %q does not match %q: %w", source, expected, sentinel)
	}
	return nil
}

func validFirstMarketplaceInstallPhase(phase Phase) bool {
	return phase == PhaseRegisteringMarketplace || phase == PhaseAddingNewPlugin || phase == PhaseRollingBack || phase == PhaseCommitting
}

func validMarketplaceUpgradePhase(phase Phase) bool {
	return phase == PhaseRemovingOldPlugin || phase == PhaseUnregisteringOldMarketplace ||
		phase == PhaseRegisteringNewMarketplace || phase == PhaseAddingNewPlugin ||
		phase == PhaseRollingBack || phase == PhaseCommitting
}

func validMarketplaceUninstallPhase(phase Phase) bool {
	return phase == PhaseRemovingPlugin || phase == PhaseRollingBack || phase == PhaseCommitting
}

func validLegacyPluginJournal(journal Journal, payload PluginJournalPayload) bool {
	if (journal.Agent != domain.AgentCodex && journal.Agent != domain.AgentClaudeCode) || payload.Marketplace != nil || payload.Filesystem != nil ||
		payload.TargetPresent && journal.Operation != OperationUninstall {
		return false
	}
	expectedTarget, err := domain.ActualPluginName(journal.Power)
	if err != nil || payload.Target.ActualName != expectedTarget || !validMarketplacePluginTarget(payload.Target) {
		return false
	}
	switch journal.Operation {
	case OperationInstall:
		if journal.Previous == nil {
			return payload.Previous == nil && !payload.PreviousPresent &&
				(journal.Phase == PhaseAddingNew || journal.Phase == PhaseRollingBack || journal.Phase == PhaseCommitting)
		}
		if err := validatePluginPreviousReferenceLegacy(journal, payload); err != nil {
			return false
		}
		if !payload.PreviousPresent && pluginMarketplaceSelector(payload.Target) != pluginMarketplaceSelector(*payload.Previous) {
			return false
		}
		if payload.PreviousPresent {
			return journal.Phase == PhaseRemovingOld || journal.Phase == PhaseAddingNew ||
				journal.Phase == PhaseRollingBack || journal.Phase == PhaseCommitting
		}
		return journal.Phase == PhaseAddingNew || journal.Phase == PhaseRollingBack || journal.Phase == PhaseCommitting
	case OperationUninstall:
		return journal.Previous == nil && payload.Previous == nil && !payload.PreviousPresent && payload.TargetPresent &&
			validUninstallPhase(journal.Phase)
	default:
		return false
	}
}

func validatePluginPreviousReferenceLegacy(journal Journal, payload PluginJournalPayload) error {
	if journal.Previous == nil || payload.Previous == nil {
		return ErrInvalidJournal
	}
	previous := *journal.Previous
	expected, err := domain.ActualPluginName(previous)
	if err != nil || previous.Name != journal.Power.Name || previous.Version == journal.Power.Version ||
		payload.Previous.ActualName != expected || !validMarketplacePluginTarget(*payload.Previous) {
		return ErrInvalidJournal
	}
	return nil
}

func validMarketplacePluginTarget(target adapter.PluginTarget) bool {
	return validPluginTarget(target) && strings.HasPrefix(target.Selector, target.ActualName+"@")
}

func pluginMarketplaceSelector(target adapter.PluginTarget) string {
	return strings.TrimPrefix(target.Selector, target.ActualName+"@")
}

// samePluginAdapterInstance 保证 Plugin 与 marketplace 写操作共享同一个 Agent 客户端实例；不可比较的值按无效请求处理。
func samePluginAdapterInstance(pluginAdapter adapter.PluginAdapter, marketplaceAdapter adapter.MarketplacePluginAdapter) bool {
	if isNilInterface(pluginAdapter) || isNilInterface(marketplaceAdapter) {
		return false
	}
	pluginValue := reflect.ValueOf(pluginAdapter)
	marketplaceValue := reflect.ValueOf(marketplaceAdapter)
	if pluginValue.Type() != marketplaceValue.Type() || pluginValue.Kind() != reflect.Pointer {
		return false
	}
	return pluginValue.Pointer() == marketplaceValue.Pointer()
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
