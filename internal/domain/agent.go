// Package domain 定义 APM 的共享领域类型。
package domain

// AgentID 表示智能体的唯一标识。
type AgentID string

// AllAgents 表示所有智能体。
const AllAgents AgentID = "all"

// AgentSnapshot 表示某一时刻可用的智能体集合。
type AgentSnapshot struct {
	Agents []AgentID
}

// Contains 报告快照是否包含指定智能体。
//
// 当前为零值占位实现。
func (s AgentSnapshot) Contains(AgentID) bool {
	return false
}

// Ordered 返回按既定顺序排列的智能体。
//
// 当前为零值占位实现。
func (s AgentSnapshot) Ordered() []AgentID {
	return nil
}
