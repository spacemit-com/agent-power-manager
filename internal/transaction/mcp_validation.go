package transaction

import (
	"fmt"

	"apm/internal/domain"
)

// validateMCPInstallRequest 校验 MCP CLI driver 的安装输入及版本切换定义。
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
	return nil
}

// validateAgentCLIJournal 校验 MCP CLI driver 的阶段与前后定义。
func validateAgentCLIJournal(journal Journal, payload AgentCLIJournalPayload) error {
	if journal.Power.Type != domain.PowerTypeMCP {
		return fmt.Errorf("agent-cli power type %q: %w", journal.Power.Type, ErrInvalidJournal)
	}
	switch journal.Operation {
	case OperationInstall:
		if payload.Server == nil {
			return fmt.Errorf("MCP install has no server definition: %w", ErrInvalidJournal)
		}
		if err := payload.Server.Validate(); err != nil {
			return fmt.Errorf("agent-cli server: %v: %w", err, ErrInvalidJournal)
		}
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
		if journal.Previous != nil || payload.Server != nil || payload.PreviousServer != nil {
			return fmt.Errorf("MCP uninstall contains definition or previous state: %w", ErrInvalidJournal)
		}
	default:
		return fmt.Errorf("operation %q: %w", journal.Operation, ErrInvalidJournal)
	}
	return nil
}
