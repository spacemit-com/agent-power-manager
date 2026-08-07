// Package domain 定义 APM 的共享领域类型。
package domain

// AgentID 表示智能体的唯一标识。
type AgentID string

const (
	// AllAgents 表示所有智能体。
	AllAgents AgentID = "all"
	// AgentCodex 表示 Codex 智能体。
	AgentCodex AgentID = "codex"
	// AgentClaudeCode 表示 Claude Code 智能体。
	AgentClaudeCode AgentID = "claude-code"
	// AgentHermes 表示 Hermes 智能体。
	AgentHermes AgentID = "hermes-agent"
)

// AgentSnapshot 表示某一时刻可用的智能体集合。
type AgentSnapshot struct {
	Agents []AgentID
}

// Contains 报告快照是否包含指定智能体。
func (s AgentSnapshot) Contains(agent AgentID) bool {
	for _, candidate := range s.Agents {
		if candidate == agent {
			return true
		}
	}
	return false
}

// Ordered 返回按既定顺序排列的智能体。
func (s AgentSnapshot) Ordered() []AgentID {
	return append([]AgentID(nil), s.Agents...)
}
