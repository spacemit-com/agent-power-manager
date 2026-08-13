package adapter

import (
	"fmt"
	"slices"

	"apm/internal/domain"
)

// Registry 按内置配置顺序保存类型专属适配器。
type Registry struct {
	ordered      []domain.AgentID
	skillByAgent map[domain.AgentID]Adapter
	mcpByAgent   map[domain.AgentID]MCPAdapter
}

// NewRegistry 创建内置 Skill 与 MCP 适配器注册表。
func NewRegistry() *Registry {
	registry := &Registry{
		ordered:      make([]domain.AgentID, 0, len(builtInAgentConfigs)),
		skillByAgent: make(map[domain.AgentID]Adapter, len(builtInAgentConfigs)),
		mcpByAgent:   make(map[domain.AgentID]MCPAdapter, len(builtInAgentConfigs)),
	}
	runner := newDefaultAgentCLIRunner()
	for _, config := range builtInAgentConfigs {
		registry.ordered = append(registry.ordered, config.id)
		registry.skillByAgent[config.id] = newSkillAdapter(config)
		switch config.id {
		case domain.AgentCodex:
			registry.mcpByAgent[config.id] = newCodexMCPAdapter(runner)
		case domain.AgentClaudeCode:
			registry.mcpByAgent[config.id] = newClaudeMCPAdapter(runner)
		case domain.AgentHermes:
			registry.mcpByAgent[config.id] = newHermesMCPAdapter(runner)
		}
	}
	return registry
}

// ResolveSkill 解析指定智能体的文件型 Skill Adapter。
func (r *Registry) ResolveSkill(agent domain.AgentID) (Adapter, error) {
	adapter, ok := r.skillByAgent[agent]
	if !ok {
		return nil, fmt.Errorf("resolve agent %q power %q: %w", agent, domain.PowerTypeSkill, ErrUnsupportedAdapter)
	}
	return adapter, nil
}

// ResolveMCP 解析指定智能体的 Agent CLI MCP Adapter。
func (r *Registry) ResolveMCP(agent domain.AgentID) (MCPAdapter, error) {
	adapter, ok := r.mcpByAgent[agent]
	if !ok {
		return nil, fmt.Errorf("resolve agent %q power %q: %w", agent, domain.PowerTypeMCP, ErrUnsupportedAdapter)
	}
	return adapter, nil
}

// RefreshInstalled 按 Agent ID 字典序探测并返回已安装智能体快照。
func (r *Registry) RefreshInstalled() (domain.AgentSnapshot, error) {
	ordered := slices.Clone(r.ordered)
	slices.Sort(ordered)
	installed := make([]domain.AgentID, 0, len(ordered))
	for _, agent := range ordered {
		adapter := r.skillByAgent[agent]
		found, err := adapter.DetectAgent()
		if err != nil {
			return domain.AgentSnapshot{}, fmt.Errorf("refresh installed agent %q: %w", agent, err)
		}
		if found {
			installed = append(installed, agent)
		}
	}
	return domain.AgentSnapshot{Agents: installed}, nil
}
