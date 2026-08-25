package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// UninstallMCP 通过目标 Agent CLI 执行可恢复卸载，并单独报告已确认缺失的注册。
func (s *Service) UninstallMCP(ctx context.Context, request MCPUninstallRequest) (UninstallResult, error) {
	result := UninstallResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateMCPUninstallRequest(request); err != nil {
		return result, err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return result, fmt.Errorf("MCP uninstall transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return result, fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return result, ErrActiveTransaction
	}
	presence, err := request.Adapter.Inspect(ctx, request.Power.Name)
	if err != nil {
		return result, fmt.Errorf("inspect MCP before uninstall: %w", err)
	}
	if presence == adapter.MCPAbsent {
		result.TargetMissing = true
		if err := s.manifests.Remove(request.Agent, domain.PowerTypeMCP, request.Power.Name); err != nil {
			return result, fmt.Errorf("remove missing MCP manifest entry: %w", err)
		}
		return result, nil
	}
	if presence != adapter.MCPPresent {
		return result, fmt.Errorf("unexpected MCP presence %q: %w", presence, ErrIndeterminateState)
	}
	journal, err := s.newMCPUninstallJournal(request)
	if err != nil {
		return result, err
	}
	if err := s.journals.Create(journal); err != nil {
		return result, fmt.Errorf("create MCP uninstall journal: %w", err)
	}
	mutation, removeErr := request.Adapter.Remove(ctx, request.Power)
	switch mutation.State {
	case adapter.MCPMutationApplied:
		journal.Phase = PhaseCommitting
		if err := s.journals.Update(journal); err != nil {
			return result, errors.Join(removeErr, fmt.Errorf("persist MCP uninstall committing direction: %w", err))
		}
		if removeErr != nil {
			return result, removeErr
		}
	case adapter.MCPMutationNotApplied:
		return result, s.finishMCPRollback(journal, mutationError("remove MCP", removeErr))
	case adapter.MCPMutationUnknown:
		return result, indeterminateMCPMutation("remove MCP", removeErr)
	default:
		return result, indeterminateMCPMutation(fmt.Sprintf("remove MCP returned state %q", mutation.State), removeErr)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.manifests.Remove(request.Agent, domain.PowerTypeMCP, request.Power.Name); err != nil {
		return result, fmt.Errorf("remove MCP manifest: %w", err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return result, fmt.Errorf("delete completed MCP uninstall journal: %w", err)
	}
	return result, nil
}

func (s *Service) newMCPUninstallJournal(request MCPUninstallRequest) (Journal, error) {
	transactionID, err := s.newID()
	if err != nil {
		return Journal{}, err
	}
	return Journal{
		SchemaVersion: 1,
		TransactionID: transactionID,
		Driver:        DriverAgentCLI,
		Operation:     OperationUninstall,
		Phase:         PhaseRemoving,
		Agent:         request.Agent,
		Power:         request.Power,
		AgentCLI:      &AgentCLIJournalPayload{},
	}, nil
}
