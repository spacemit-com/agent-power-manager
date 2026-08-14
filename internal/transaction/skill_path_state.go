package transaction

import (
	"errors"
	"fmt"
	"os"
)

// PathState 表示事务相关路径的存在状态。
type PathState struct {
	StagingExists    bool
	TargetExists     bool
	BackupExists     bool
	QuarantineExists bool
}

// InspectPathState 检查事务日志对应路径的状态。
func InspectPathState(journal Journal) (PathState, error) {
	return inspectPathState(journal)
}

func inspectPathState(journal Journal) (PathState, error) {
	var state PathState
	filesystem := journal.Filesystem
	checks := []struct {
		name   string
		path   string
		exists *bool
	}{
		{name: "staging", path: filesystem.StagingRoot, exists: &state.StagingExists},
		{name: "target", path: filesystem.TargetPath, exists: &state.TargetExists},
		{name: "backup", path: filesystem.BackupPath, exists: &state.BackupExists},
		{name: "quarantine", path: filesystem.QuarantinePath, exists: &state.QuarantineExists},
	}
	for _, check := range checks {
		if check.path == "" {
			continue
		}
		_, err := os.Lstat(check.path)
		switch {
		case err == nil:
			*check.exists = true
		case errors.Is(err, os.ErrNotExist):
		default:
			return PathState{}, fmt.Errorf("inspect %s path %q: %w", check.name, check.path, err)
		}
	}
	return state, nil
}

// recoveryDirection 根据持久化阶段和路径状态固定事务恢复方向。
func recoveryDirection(journal Journal, state PathState) (RecoveryDirection, error) {
	switch journal.Operation {
	case OperationInstall:
		if (journal.Phase == PhasePreparing || journal.Phase == PhaseRollingBack) && state.TargetExists && state.BackupExists {
			return "", fmt.Errorf("install target and backup both exist during rollback: %w", ErrIndeterminateState)
		}
		switch journal.Phase {
		case PhasePreparing, PhaseRollingBack:
			return RecoveryRollback, nil
		case PhasePublishing:
			if state.TargetExists {
				return RecoveryCommit, nil
			}
			return RecoveryRollback, nil
		case PhaseCommitting:
			return RecoveryCommit, nil
		}
	case OperationUninstall:
		if state.TargetExists && state.QuarantineExists {
			return "", fmt.Errorf("uninstall target and quarantine both exist: %w", ErrIndeterminateState)
		}
		switch journal.Phase {
		case PhaseRollingBack:
			return RecoveryRollback, nil
		case PhaseRemoving:
			if state.QuarantineExists || !state.TargetExists {
				return RecoveryCommit, nil
			}
			return RecoveryRollback, nil
		case PhaseCommitting:
			return RecoveryCommit, nil
		}
	}
	return "", fmt.Errorf("operation %q phase %q: %w", journal.Operation, journal.Phase, ErrIndeterminateState)
}
