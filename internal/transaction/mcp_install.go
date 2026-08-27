package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// InstallMCP 通过目标 Agent CLI 执行可恢复的 MCP 首次安装或版本切换。
func (s *Service) InstallMCP(ctx context.Context, request MCPInstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateMCPInstallRequest(request); err != nil {
		return err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return fmt.Errorf("MCP install transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return ErrActiveTransaction
	}
	presence, err := request.Adapter.Inspect(ctx, request.Power.Name)
	if err != nil {
		return fmt.Errorf("inspect MCP before install: %w", err)
	}
	if request.Previous == nil {
		if presence == adapter.MCPPresent {
			return fmt.Errorf("unmanaged MCP %q is already registered: %w", request.Power.Name, errors.Join(adapter.ErrMCPConflict, adapter.ErrTargetConflict))
		}
		if presence != adapter.MCPAbsent {
			return fmt.Errorf("unexpected MCP presence %q: %w", presence, ErrIndeterminateState)
		}
		if request.Server == nil {
			return fmt.Errorf("first MCP install requires server definition: %w", ErrInvalidRequest)
		}
		return s.installNewMCP(ctx, request)
	}
	if presence == adapter.MCPAbsent {
		removeErr := s.manifests.Remove(request.Agent, domain.PowerTypeMCP, request.Power.Name)
		staleErr := fmt.Errorf("removed stale MCP manifest entry for agent %q power %q: %w", request.Agent, request.Power.Name, ErrStaleManifest)
		if removeErr != nil {
			return errors.Join(staleErr, fmt.Errorf("remove stale MCP manifest entry: %w", removeErr))
		}
		return staleErr
	}
	if presence != adapter.MCPPresent {
		return fmt.Errorf("unexpected MCP presence %q: %w", presence, ErrIndeterminateState)
	}
	if request.Server == nil || request.PreviousServer == nil {
		return fmt.Errorf("MCP update requires old and new definitions: %w", ErrInvalidRequest)
	}
	return s.updateMCP(ctx, request)
}

func (s *Service) installNewMCP(ctx context.Context, request MCPInstallRequest) error {
	journal, err := s.newMCPInstallJournal(request, PhaseAddingNew)
	if err != nil {
		return err
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create MCP install journal: %w", err)
	}
	result, addErr := request.Adapter.Add(ctx, request.Power, *journal.AgentCLI.Server)
	return s.finishMCPAdd(ctx, journal, request.Adapter, result, addErr)
}

func (s *Service) updateMCP(ctx context.Context, request MCPInstallRequest) error {
	journal, err := s.newMCPInstallJournal(request, PhaseRemovingOld)
	if err != nil {
		return err
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create MCP update journal: %w", err)
	}
	result, removeErr := request.Adapter.Remove(ctx, *journal.Previous)
	switch result.State {
	case adapter.MCPMutationApplied:
		journal.Phase = PhaseAddingNew
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(removeErr, fmt.Errorf("persist MCP adding-new intent: %w", err))
		}
		if removeErr != nil {
			return removeErr
		}
	case adapter.MCPMutationNotApplied:
		return s.finishMCPRollback(journal, mutationError("remove old MCP", removeErr))
	case adapter.MCPMutationUnknown:
		return indeterminateMCPMutation("remove old MCP", removeErr)
	default:
		return indeterminateMCPMutation(fmt.Sprintf("remove old MCP returned state %q", result.State), removeErr)
	}

	result, addErr := request.Adapter.Add(ctx, request.Power, *journal.AgentCLI.Server)
	return s.finishMCPAdd(ctx, journal, request.Adapter, result, addErr)
}

func (s *Service) finishMCPAdd(ctx context.Context, journal Journal, mcpAdapter adapter.MCPAdapter, result adapter.MCPMutationResult, addErr error) error {
	switch result.State {
	case adapter.MCPMutationApplied:
		journal.Phase = PhaseCommitting
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(addErr, fmt.Errorf("persist MCP install committing direction: %w", err))
		}
		if addErr != nil {
			return addErr
		}
	case adapter.MCPMutationNotApplied:
		originalErr := mutationError("add MCP", addErr)
		if journal.Previous == nil {
			return s.finishMCPRollback(journal, originalErr)
		}
		journal.Phase = PhaseRollingBack
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(originalErr, fmt.Errorf("persist MCP rollback direction: %w", err))
		}
		restoreResult, restoreErr := mcpAdapter.Add(ctx, *journal.Previous, *journal.AgentCLI.PreviousServer)
		if restoreResult.State == adapter.MCPMutationApplied {
			if restoreErr != nil {
				return errors.Join(originalErr, restoreErr)
			}
		} else if restoreResult.State == adapter.MCPMutationUnknown {
			return errors.Join(originalErr, indeterminateMCPMutation("restore old MCP", restoreErr))
		} else {
			return errors.Join(originalErr, mutationError("restore old MCP", restoreErr))
		}
		if err := s.journals.Delete(journal.TransactionID); err != nil {
			return errors.Join(originalErr, fmt.Errorf("delete rolled-back MCP journal: %w", err))
		}
		return originalErr
	case adapter.MCPMutationUnknown:
		return indeterminateMCPMutation("add MCP", addErr)
	default:
		return indeterminateMCPMutation(fmt.Sprintf("add MCP returned state %q", result.State), addErr)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Set(journal.Agent, journal.installedPower()); err != nil {
		return fmt.Errorf("set MCP manifest: %w", err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return fmt.Errorf("delete completed MCP install journal: %w", err)
	}
	return nil
}

func (s *Service) finishMCPRollback(journal Journal, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist MCP rollback direction: %w", err))
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return errors.Join(originalErr, fmt.Errorf("delete rolled-back MCP journal: %w", err))
	}
	return originalErr
}

func (s *Service) newMCPInstallJournal(request MCPInstallRequest, phase Phase) (Journal, error) {
	transactionID, err := s.newID()
	if err != nil {
		return Journal{}, err
	}
	server := request.Server.Clone()
	var previousServer *domain.MCPServer
	if request.PreviousServer != nil {
		clone := request.PreviousServer.Clone()
		previousServer = &clone
	}
	return Journal{
		SchemaVersion: 1,
		TransactionID: transactionID,
		Driver:        DriverAgentCLI,
		Operation:     OperationInstall,
		Phase:         phase,
		Agent:         request.Agent,
		Power:         request.Power,
		Description:   request.Description,
		Tags:          request.Tags,
		Previous:      clonePowerRef(request.Previous),
		AgentCLI: &AgentCLIJournalPayload{
			Server:         &server,
			PreviousServer: previousServer,
		},
	}, nil
}

func mutationError(operation string, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("%s was not applied", operation)
}

func indeterminateMCPMutation(operation string, err error) error {
	return errors.Join(fmt.Errorf("%s has unknown result", operation), err, ErrIndeterminateState)
}
