// Package transaction 提供能力安装和卸载的一致性事务与恢复能力。
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

// Driver 表示事务副作用由哪一类恢复驱动负责处理。
type Driver string

const (
	// DriverFilesystem 表示通过文件系统发布和隔离目录恢复 Skill。
	DriverFilesystem Driver = "filesystem"
	// DriverAgentCLI 表示通过目标 Agent CLI 恢复 MCP 注册。
	DriverAgentCLI Driver = "agent-cli"
)

// Phase 表示事务所处阶段。
type Phase string

const (
	// PhasePreparing 表示安装准备阶段。
	PhasePreparing Phase = "preparing"
	// PhasePublishing 表示安装发布意图已持久化。
	PhasePublishing Phase = "publishing"
	// PhaseRemovingOld 表示 MCP 更新即将移除旧注册。
	PhaseRemovingOld Phase = "removing-old"
	// PhaseAddingNew 表示 MCP 安装或更新即将添加新注册。
	PhaseAddingNew Phase = "adding-new"
	// PhaseRemoving 表示卸载移除意图已持久化。
	PhaseRemoving Phase = "removing"
	// PhaseRollingBack 表示事务方向已固定为回滚。
	PhaseRollingBack Phase = "rolling-back"
	// PhaseCommitting 表示事务方向已固定为完成提交。
	PhaseCommitting Phase = "committing"
)

// FilesystemJournalPayload 保存文件系统事务恢复所需的受控路径。
type FilesystemJournalPayload struct {
	StagingRoot    string `json:"staging_root,omitempty"`
	TargetPath     string `json:"target_path"`
	TransactionDir string `json:"transaction_dir"`
	BackupPath     string `json:"backup_path,omitempty"`
	QuarantinePath string `json:"quarantine_path,omitempty"`
}

// AgentCLIJournalPayload 保存 MCP CLI 事务恢复所需的新旧启动定义。
type AgentCLIJournalPayload struct {
	Server         domain.MCPServer  `json:"server"`
	PreviousServer *domain.MCPServer `json:"previous_server,omitempty"`
}

// Journal 表示恢复唯一活动事务所需的持久化状态。
type Journal struct {
	SchemaVersion int                       `json:"schema_version"`
	TransactionID string                    `json:"transaction_id"`
	Driver        Driver                    `json:"driver"`
	Operation     Operation                 `json:"operation"`
	Phase         Phase                     `json:"phase"`
	Agent         domain.AgentID            `json:"agent"`
	Power         domain.PowerRef           `json:"power"`
	Previous      *domain.PowerRef          `json:"previous,omitempty"`
	Filesystem    *FilesystemJournalPayload `json:"filesystem,omitempty"`
	AgentCLI      *AgentCLIJournalPayload   `json:"agent_cli,omitempty"`
}

// RecoveryDirection 表示恢复器固定的收敛方向。
type RecoveryDirection string

const (
	// RecoveryRollback 表示恢复旧状态。
	RecoveryRollback RecoveryDirection = "rollback"
	// RecoveryCommit 表示完成新状态提交。
	RecoveryCommit RecoveryDirection = "commit"
)

// RecoveryActionKind 表示恢复器已完成的单项修复。
type RecoveryActionKind string

const (
	RecoveryActionRestoreTarget        RecoveryActionKind = "restore-target"
	RecoveryActionSetManifest          RecoveryActionKind = "set-manifest"
	RecoveryActionRemoveManifest       RecoveryActionKind = "remove-manifest"
	RecoveryActionRemoveBackup         RecoveryActionKind = "remove-backup"
	RecoveryActionRemoveQuarantine     RecoveryActionKind = "remove-quarantine"
	RecoveryActionRemoveStaging        RecoveryActionKind = "remove-staging"
	RecoveryActionRemoveTransactionDir RecoveryActionKind = "remove-transaction-dir"
	RecoveryActionAddMCP               RecoveryActionKind = "add-mcp"
	RecoveryActionRemoveMCP            RecoveryActionKind = "remove-mcp"
)

// RecoveryAction 描述一次已完成的结构化恢复动作。
type RecoveryAction struct {
	Kind RecoveryActionKind
	Path string
}

// RecoveryResult 描述公共预检恢复的方向与已完成动作。
type RecoveryResult struct {
	Recovered     bool
	TransactionID string
	Operation     Operation
	Agent         domain.AgentID
	Power         domain.PowerRef
	OriginalPhase Phase
	Direction     RecoveryDirection
	Actions       []RecoveryAction
}
