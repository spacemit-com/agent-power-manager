package transaction

import (
	"fmt"

	"apm/internal/domain"
)

// validateAgentInstallRequest 校验文件型 Agent 安装事务的完整输入边界。
func validateAgentInstallRequest(request AgentInstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate install request: %w", err)
	}
	if request.Power.Type != domain.PowerTypeExpert {
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

// validateAgentUninstallRequest 校验文件型 Agent 卸载事务的完整输入边界。
func validateAgentUninstallRequest(request AgentUninstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate uninstall request: %w", err)
	}
	if request.Power.Type != domain.PowerTypeExpert {
		return fmt.Errorf("filesystem uninstall power type %q: %w", request.Power.Type, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate uninstall request: nil adapter: %w", ErrInvalidRequest)
	}
	return nil
}
