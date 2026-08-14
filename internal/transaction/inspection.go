package transaction

import (
	"context"
	"fmt"

	"apm/internal/domain"
)

// Inspector 定义只读检查活动事务的能力。
type Inspector interface {
	Inspect(context.Context) (InspectionResult, error)
}

// Recoverer 定义收敛活动事务的能力。
type Recoverer interface {
	Recover(context.Context) (RecoveryResult, error)
}

// InspectionResult 描述活动事务的只读快照。
type InspectionResult struct {
	Active        bool
	TransactionID string
	Operation     Operation
	Agent         domain.AgentID
	Power         domain.PowerRef
	OriginalPhase Phase
}

// Inspect 读取活动事务但不执行恢复或其他写入；底层本地 Store 读取同步执行，不能由 ctx 强制取消。
func (s *Service) Inspect(ctx context.Context) (InspectionResult, error) {
	_, result, err := s.inspectActiveJournal(ctx)
	return result, err
}

// inspectActiveJournal 统一执行一次 Store 读取、校验和只读结果投影。
func (s *Service) inspectActiveJournal(_ context.Context) (Journal, InspectionResult, error) {
	if isNilInterface(s.journals) {
		return Journal{}, InspectionResult{}, fmt.Errorf("inspect transaction with nil journal store: %w", ErrInvalidRequest)
	}
	journal, found, err := s.journals.Inspect()
	if err != nil {
		return Journal{}, InspectionResult{}, fmt.Errorf("inspect active transaction: %w", err)
	}
	if !found {
		return Journal{}, InspectionResult{}, nil
	}
	if err := validateJournal(journal); err != nil {
		return Journal{}, InspectionResult{}, err
	}
	result := InspectionResult{
		Active:        true,
		TransactionID: journal.TransactionID,
		Operation:     journal.Operation,
		Agent:         journal.Agent,
		Power:         journal.Power,
		OriginalPhase: journal.Phase,
	}
	return journal, result, nil
}
