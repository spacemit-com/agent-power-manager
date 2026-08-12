package transaction

import (
	"context"
	"fmt"

	"apm/internal/adapter"
)

// recoverMCP 只使用 journal 定义和目标 Agent CLI，将 agent-cli 事务收敛到固定方向。
func (s *Service) recoverMCP(ctx context.Context, journal Journal, result *RecoveryResult) error {
	if journal.Phase == PhaseCommitting {
		result.Direction = RecoveryCommit
		return s.commitRecoveredMCP(journal, result)
	}
	if isNilInterface(s.mcpAdapters) {
		return fmt.Errorf("MCP recovery resolver is nil: %w", ErrInvalidRequest)
	}
	mcpAdapter, err := s.mcpAdapters.ResolveMCP(journal.Agent)
	if err != nil {
		return fmt.Errorf("resolve MCP adapter for %q: %w", journal.Agent, err)
	}
	if isNilInterface(mcpAdapter) {
		return fmt.Errorf("resolved nil MCP adapter for %q: %w", journal.Agent, ErrInvalidRequest)
	}
	presence, err := mcpAdapter.Inspect(ctx, journal.Power.Name)
	if err != nil {
		return fmt.Errorf("inspect MCP recovery state: %w", err)
	}

	switch journal.Operation {
	case OperationInstall:
		return s.recoverMCPInstall(ctx, journal, presence, mcpAdapter, result)
	case OperationUninstall:
		return s.recoverMCPUninstall(ctx, journal, presence, mcpAdapter, result)
	default:
		return fmt.Errorf("MCP recovery operation %q: %w", journal.Operation, ErrIndeterminateState)
	}
}

func (s *Service) recoverMCPInstall(ctx context.Context, journal Journal, presence adapter.MCPPresence, mcpAdapter adapter.MCPAdapter, result *RecoveryResult) error {
	switch journal.Phase {
	case PhaseAddingNew:
		if presence == adapter.MCPPresent {
			return s.commitRecoveredMCPInstall(journal, result)
		}
		return s.rollbackRecoveredMCPInstall(ctx, journal, presence, mcpAdapter, result)
	case PhaseRemovingOld:
		return s.rollbackRecoveredMCPInstall(ctx, journal, presence, mcpAdapter, result)
	case PhaseRollingBack:
		return s.rollbackRecoveredMCPInstall(ctx, journal, presence, mcpAdapter, result)
	default:
		return fmt.Errorf("MCP install recovery phase %q: %w", journal.Phase, ErrIndeterminateState)
	}
}

func (s *Service) rollbackRecoveredMCPInstall(ctx context.Context, journal Journal, presence adapter.MCPPresence, mcpAdapter adapter.MCPAdapter, result *RecoveryResult) error {
	result.Direction = RecoveryRollback
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if journal.Previous == nil {
		if presence == adapter.MCPPresent {
			mutation, err := mcpAdapter.Remove(ctx, journal.Power)
			if err := requireAppliedRecoveryMutation("remove rolled-back new MCP", mutation, err); err != nil {
				return err
			}
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveMCP})
		}
		return s.deleteRecoveredJournal(journal)
	}
	if presence == adapter.MCPAbsent {
		mutation, err := mcpAdapter.Add(ctx, *journal.Previous, *journal.AgentCLI.PreviousServer)
		if err := requireAppliedRecoveryMutation("restore previous MCP", mutation, err); err != nil {
			return err
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionAddMCP})
	}
	return s.deleteRecoveredJournal(journal)
}

func (s *Service) commitRecoveredMCPInstall(journal Journal, result *RecoveryResult) error {
	result.Direction = RecoveryCommit
	if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
		return err
	}
	return s.commitRecoveredMCP(journal, result)
}

func (s *Service) recoverMCPUninstall(ctx context.Context, journal Journal, presence adapter.MCPPresence, mcpAdapter adapter.MCPAdapter, result *RecoveryResult) error {
	switch journal.Phase {
	case PhaseRemoving:
		if presence == adapter.MCPAbsent {
			result.Direction = RecoveryCommit
			if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
				return err
			}
			return s.commitRecoveredMCP(journal, result)
		}
		result.Direction = RecoveryRollback
		if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
			return err
		}
		return s.deleteRecoveredJournal(journal)
	case PhaseRollingBack:
		result.Direction = RecoveryRollback
		if presence == adapter.MCPAbsent {
			mutation, err := mcpAdapter.Add(ctx, journal.Power, journal.AgentCLI.Server)
			if err := requireAppliedRecoveryMutation("restore uninstalled MCP", mutation, err); err != nil {
				return err
			}
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionAddMCP})
		}
		return s.deleteRecoveredJournal(journal)
	default:
		return fmt.Errorf("MCP uninstall recovery phase %q: %w", journal.Phase, ErrIndeterminateState)
	}
}

func (s *Service) commitRecoveredMCP(journal Journal, result *RecoveryResult) error {
	var err error
	if journal.Operation == OperationInstall {
		err = s.manifests.Set(journal.Agent, journal.Power)
		if err == nil {
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionSetManifest})
		}
	} else {
		err = s.manifests.Remove(journal.Agent, journal.Power.Type, journal.Power.Name)
		if err == nil {
			result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemoveManifest})
		}
	}
	if err != nil {
		return fmt.Errorf("commit recovered MCP manifest: %w", err)
	}
	return s.deleteRecoveredJournal(journal)
}

func requireAppliedRecoveryMutation(operation string, mutation adapter.MCPMutationResult, err error) error {
	switch mutation.State {
	case adapter.MCPMutationApplied:
		return err
	case adapter.MCPMutationNotApplied:
		return mutationError(operation, err)
	case adapter.MCPMutationUnknown:
		return indeterminateMCPMutation(operation, err)
	default:
		return indeterminateMCPMutation(fmt.Sprintf("%s returned state %q", operation, mutation.State), err)
	}
}
