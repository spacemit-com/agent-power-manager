package adapter

import "apm/internal/domain"

// AgentInstallResult 报告 Agent Adapter 安装是否已经越过原子发布提交点。
type AgentInstallResult struct {
	Published bool
}

// AgentAdapter 定义智能体的目录型 Agent（Bibit 子代理）安装操作。
// artifact 始终是完整目录（agent.yaml + assets/），TargetPath 返回目录路径，
// 不发布或改写 artifact 之外的任何文件。
type AgentAdapter interface {
	// TargetPath 返回最终安装目录路径 root/agents/<ref.Name>，不含版本目录。
	TargetPath(domain.PowerRef) (string, error)
	// ValidateArtifact 只读校验完整 Agent 目录（物理安全、根闭集、avatar PNG
	// 与 agent.name/version 身份），可用于 staging/target/backup/quarantine
	// 目录，不发布或改写任何文件。
	ValidateArtifact(domain.PowerRef, string) error
	// Install 校验并原子发布 staging/agents/<ref.Name> 完整目录到 TargetPath；
	// 已存在目标返回 ErrTargetConflict，发布前失败不留下任何可见输出。
	Install(domain.PowerRef, string) (AgentInstallResult, error)
}
