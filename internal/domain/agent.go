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
	// AgentBibit 表示 Bibit 智能体。
	AgentBibit AgentID = "bibit"
)

// KnownAgentIDs 返回 `--agent` 标志可接受的智能体标识：特殊值 `all` 在前，其余按字典序排列。
func KnownAgentIDs() []AgentID {
	return []AgentID{AllAgents, AgentBibit, AgentClaudeCode, AgentCodex, AgentHermes}
}

// IsKnownAgentID 报告 id 是否为 `--agent` 标志可接受的智能体标识。
func IsKnownAgentID(id AgentID) bool {
	for _, candidate := range KnownAgentIDs() {
		if candidate == id {
			return true
		}
	}
	return false
}

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
