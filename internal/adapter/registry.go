package adapter

import (
	"fmt"
	"slices"

	"apm/internal/domain"
)

// Registry 按内置配置顺序保存 Skill 适配器。
type Registry struct {
	ordered []domain.AgentID
	byAgent map[domain.AgentID]Adapter
}

// NewRegistry 创建内置 Skill 适配器注册表。
func NewRegistry() *Registry {
	registry := &Registry{
		ordered: make([]domain.AgentID, 0, len(builtInAgentConfigs)),
		byAgent: make(map[domain.AgentID]Adapter, len(builtInAgentConfigs)),
	}
	for _, config := range builtInAgentConfigs {
		registry.ordered = append(registry.ordered, config.id)
		registry.byAgent[config.id] = newSkillAdapter(config)
	}
	return registry
}

// Resolve 解析指定智能体和能力类别的适配器。
func (r *Registry) Resolve(agent domain.AgentID, power domain.PowerType) (Adapter, error) {
	if power != domain.PowerTypeSkill {
		return nil, fmt.Errorf("resolve agent %q power %q: %w", agent, power, ErrUnsupportedAdapter)
	}
	adapter, ok := r.byAgent[agent]
	if !ok {
		return nil, fmt.Errorf("resolve agent %q power %q: %w", agent, power, ErrUnsupportedAdapter)
	}
	return adapter, nil
}

// RefreshInstalled 按 Agent ID 字典序探测并返回已安装智能体快照。
func (r *Registry) RefreshInstalled() (domain.AgentSnapshot, error) {
	ordered := slices.Clone(r.ordered)
	slices.Sort(ordered)
	installed := make([]domain.AgentID, 0, len(ordered))
	for _, agent := range ordered {
		adapter := r.byAgent[agent]
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
