package transaction

import (
	"context"
	"errors"
	"fmt"
)

// Recover 检查并收敛任意上次未完成的集中事务。
func (s *Service) Recover(ctx context.Context) (RecoveryResult, error) {
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return RecoveryResult{}, recoveryError(fmt.Errorf("recovery dependencies are nil: %w", ErrInvalidRequest))
	}
	journal, found, err := s.journals.Inspect()
	if err != nil {
		return RecoveryResult{}, recoveryError(fmt.Errorf("inspect active transaction: %w", err))
	}
	if !found {
		return RecoveryResult{}, nil
	}
	result := RecoveryResult{
		TransactionID: journal.TransactionID,
		Operation:     journal.Operation,
		Agent:         journal.Agent,
		Power:         journal.Power,
		OriginalPhase: journal.Phase,
	}
	if err := validateJournal(journal); err != nil {
		return result, recoveryError(err)
	}
	if err := ctx.Err(); err != nil {
		return result, recoveryError(err)
	}
	switch journal.Driver {
	case DriverFilesystem:
		err = s.recoverSkill(ctx, journal, &result)
	case DriverAgentCLI:
		err = s.recoverMCP(ctx, journal, &result)
	case DriverPlugin:
		err = s.recoverPlugin(ctx, journal, &result)
	}
	if err != nil {
		return result, recoveryError(err)
	}
	result.Recovered = true
	return result, nil
}

// persistRecoveryDirection 在任何恢复变更前持久化不可逆方向。
func (s *Service) persistRecoveryDirection(journal *Journal, phase Phase) error {
	if journal.Phase == phase {
		return nil
	}
	journal.Phase = phase
	if err := s.journals.Update(*journal); err != nil {
		return fmt.Errorf("persist recovery direction %q: %w", phase, err)
	}
	return nil
}

func (s *Service) deleteRecoveredJournal(journal Journal) error {
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return fmt.Errorf("delete recovered transaction journal: %w", err)
	}
	return nil
}

func recoveryError(err error) error {
	return errors.Join(ErrRecoveryFailed, err)
}
