// Package transaction 提供能力安装和卸载事务的占位类型。
package transaction

import "apm/internal/domain"

// Operation 表示事务操作类型。
type Operation string

const (
	// OperationInstall 表示安装操作。
	OperationInstall Operation = "install"
	// OperationUninstall 表示卸载操作。
	OperationUninstall Operation = "uninstall"
)

// Phase 表示事务所处阶段。
type Phase string

const (
	// PhasePreparing 表示准备阶段。
	PhasePreparing Phase = "preparing"
	// PhasePublishing 表示发布阶段。
	PhasePublishing Phase = "publishing"
	// PhaseRemoving 表示移除阶段。
	PhaseRemoving Phase = "removing"
)

// Journal 表示事务日志条目。
type Journal struct {
	Operation Operation
	Agent     domain.AgentID
	Power     domain.PowerRef
	Version   string
	Phase     Phase
}
