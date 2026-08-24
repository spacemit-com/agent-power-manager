package adapter

import "apm/internal/domain"

// AgentInstallResult 报告 Agent Adapter 安装是否已经越过原子发布提交点。
type AgentInstallResult struct {
	Published bool
}

// AgentAdapter 定义智能体的文件型 Agent（Bibit 子代理）安装操作。
type AgentAdapter interface {
	TargetPath(domain.PowerRef) (string, error)
	// ValidateArtifact 只读校验 artifact 的 agent.yaml 身份与 ref 完全一致，可用于
	// staging/target/backup/quarantine 文件，不发布或改写任何文件。
	ValidateArtifact(domain.PowerRef, string) error
	Install(domain.PowerRef, string) (AgentInstallResult, error)
}
