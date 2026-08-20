package adapter

import "apm/internal/domain"

// AgentInstallResult 报告 Agent Adapter 安装是否已经越过原子发布提交点。
type AgentInstallResult struct {
	Published bool
}

// AgentAdapter 定义智能体的文件型 Agent（Bibit 子代理）安装操作。
type AgentAdapter interface {
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (AgentInstallResult, error)
}
