package adapter

import (
	"fmt"
	"slices"

	"apm/internal/domain"
)

// agentRegistration 声明一个内置 Agent 的 profile 与类型专属 Adapter factory。
// factory 为空表示该 Agent 不支持对应 Power。
type agentRegistration struct {
	profile   agentProfile
	newSkill  func(agentProfile) SkillAdapter
	newMCP    func(agentCLIRunner) MCPAdapter
	newPlugin func(agentProfile, agentCLIRunner) PluginAdapter
}

var builtInAgentRegistrations = []agentRegistration{
	{
		profile:  agentProfile{id: domain.AgentCodex, homeEnv: "CODEX_HOME", defaultRoot: ".codex", adaptSkill: noOpSkillAdapt},
		newSkill: func(profile agentProfile) SkillAdapter { return newFilesystemSkillAdapter(profile) },
		newMCP:   func(runner agentCLIRunner) MCPAdapter { return newCodexMCPAdapter(runner) },
		newPlugin: func(_ agentProfile, runner agentCLIRunner) PluginAdapter {
			return newCodexPluginAdapter(runner)
		},
	},
	{
		profile:  agentProfile{id: domain.AgentClaudeCode, homeEnv: "CLAUDE_CONFIG_DIR", defaultRoot: ".claude", adaptSkill: noOpSkillAdapt},
		newSkill: func(profile agentProfile) SkillAdapter { return newFilesystemSkillAdapter(profile) },
		newMCP:   func(runner agentCLIRunner) MCPAdapter { return newClaudeMCPAdapter(runner) },
		newPlugin: func(_ agentProfile, runner agentCLIRunner) PluginAdapter {
			return newClaudePluginAdapter(runner)
		},
	},
	{
		profile:  agentProfile{id: domain.AgentHermes, homeEnv: "HERMES_HOME", defaultRoot: ".hermes", adaptSkill: noOpSkillAdapt},
		newSkill: func(profile agentProfile) SkillAdapter { return newFilesystemSkillAdapter(profile) },
		newMCP:   func(runner agentCLIRunner) MCPAdapter { return newHermesMCPAdapter(runner) },
		newPlugin: func(profile agentProfile, runner agentCLIRunner) PluginAdapter {
			return newHermesPluginAdapter(runner, profile)
		},
	},
}

// Registry 按声明顺序保存 Agent profile 与类型专属 Adapter。
type Registry struct {
	ordered       []domain.AgentID
	profiles      map[domain.AgentID]agentProfile
	skillByAgent  map[domain.AgentID]SkillAdapter
	mcpByAgent    map[domain.AgentID]MCPAdapter
	pluginByAgent map[domain.AgentID]PluginAdapter
}

// NewRegistry 创建内置 Skill、MCP 与 Plugin 类型专属适配器注册表。
func NewRegistry() *Registry {
	registry := &Registry{
		ordered:       make([]domain.AgentID, 0, len(builtInAgentRegistrations)),
		profiles:      make(map[domain.AgentID]agentProfile, len(builtInAgentRegistrations)),
		skillByAgent:  make(map[domain.AgentID]SkillAdapter, len(builtInAgentRegistrations)),
		mcpByAgent:    make(map[domain.AgentID]MCPAdapter, len(builtInAgentRegistrations)),
		pluginByAgent: make(map[domain.AgentID]PluginAdapter, len(builtInAgentRegistrations)),
	}
	runner := newDefaultAgentCLIRunner()
	for _, registration := range builtInAgentRegistrations {
		agent := registration.profile.id
		registry.ordered = append(registry.ordered, agent)
		registry.profiles[agent] = registration.profile
		if registration.newSkill != nil {
			registry.skillByAgent[agent] = registration.newSkill(registration.profile)
		}
		if registration.newMCP != nil {
			registry.mcpByAgent[agent] = registration.newMCP(runner)
		}
		if registration.newPlugin != nil {
			registry.pluginByAgent[agent] = registration.newPlugin(registration.profile, runner)
		}
	}
	return registry
}

// ResolvePlugin 解析指定智能体的 Plugin 类型专属 Adapter。
func (r *Registry) ResolvePlugin(agent domain.AgentID) (PluginAdapter, error) {
	adapter, ok := r.pluginByAgent[agent]
	if !ok {
		return nil, fmt.Errorf("resolve agent %q power %q: %w", agent, domain.PowerTypePlugin, ErrUnsupportedAdapter)
	}
	return adapter, nil
}

// ResolveSkill 解析指定智能体的文件型 Skill Adapter。
func (r *Registry) ResolveSkill(agent domain.AgentID) (SkillAdapter, error) {
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
		profile, ok := r.profiles[agent]
		if !ok {
			return domain.AgentSnapshot{}, fmt.Errorf("refresh installed agent %q: missing Agent profile", agent)
		}
		found, err := profile.detectInstalled()
		if err != nil {
			return domain.AgentSnapshot{}, fmt.Errorf("refresh installed agent %q: %w", agent, err)
		}
		if found {
			installed = append(installed, agent)
		}
	}
	return domain.AgentSnapshot{Agents: installed}, nil
}
