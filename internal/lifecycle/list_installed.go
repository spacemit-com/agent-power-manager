package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/cli"
	"apm/internal/domain"
)

// agentPowerGroups 保存单个 Agent 的已安装能力分组。
type agentPowerGroups struct {
	agent  domain.AgentID
	groups domain.InstalledPowerGroups
}

// installedListAgents 解析具体 Agent 或 --type agent 的目标集合；非 agent 类型省略 --agent 视为 all。
func installedListAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	if flags.Type == string(domain.PowerTypeExpert) {
		if flags.Agent != "" && domain.AgentID(flags.Agent) != domain.AgentBibit {
			return nil, fmt.Errorf("list installed power type %q only supports agent %q", domain.PowerTypeExpert, domain.AgentBibit)
		}
		if !snapshot.Contains(domain.AgentBibit) {
			return nil, fmt.Errorf("list installed powers for agent %q: unsupported or not installed", domain.AgentBibit)
		}
		return []domain.AgentID{domain.AgentBibit}, nil
	}
	target := domain.AgentID(flags.Agent)
	if target == "" || target == domain.AllAgents {
		return snapshot.Ordered(), nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("list installed powers for agent %q: unsupported or not installed%s", target, availableAgentSuffix(flags, snapshot))
	}
	return []domain.AgentID{target}, nil
}

// isAllInstalledList 报告 installed list 是否进入全部受支持 Agent 的分组模式。
func isAllInstalledList(flags cli.Flags) bool {
	return flags.Type != string(domain.PowerTypeExpert) && (flags.Agent == "" || domain.AgentID(flags.Agent) == domain.AllAgents)
}

// listInstalled 无锁读取 Agent 快照和配置，生成 best-effort 的已安装清单。
// all 模式遍历全部受支持 Agent 并区分未安装/空清单；具体 Agent 模式只读取目标。
func (s *ListService) listInstalled(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
	snapshot, err := s.registry.RefreshInstalled()
	if err != nil {
		return cli.ListResult{}, fmt.Errorf("refresh installed agents for list: %w", err)
	}
	if isAllInstalledList(flags) {
		return s.listAllInstalled(flags, snapshot)
	}
	agents, err := installedListAgents(flags, snapshot)
	if err != nil {
		return cli.ListResult{}, err
	}
	return s.listConcreteInstalled(flags, agents[0])
}

// listConcreteInstalled 读取单个 Agent 的已安装清单并生成扁平结果。
func (s *ListService) listConcreteInstalled(flags cli.Flags, agent domain.AgentID) (cli.ListResult, error) {
	groups, err := s.config.List(agent)
	if err != nil {
		return cli.ListResult{}, fmt.Errorf("list installed powers for agent %q: %w", agent, err)
	}
	return makeInstalledListResult(flags, []agentPowerGroups{{agent: agent, groups: groups}}), nil
}

// listAllInstalled 遍历全部受支持 Agent，按顺序生成状态摘要与过滤后的能力结果。
func (s *ListService) listAllInstalled(flags cli.Flags, snapshot domain.AgentSnapshot) (cli.ListResult, error) {
	supported := s.registry.SupportedAgents()
	powers := make([]cli.ListedPower, 0)
	agents := make([]cli.InstalledAgentSummary, 0, len(supported))
	failures := make([]cli.AgentFailure, 0)
	for _, agent := range supported {
		if !snapshot.Contains(agent) {
			agents = append(agents, cli.InstalledAgentSummary{Agent: agent, State: cli.InstalledAgentNotInstalled})
			continue
		}
		groups, listErr := s.config.List(agent)
		if listErr != nil {
			failures = append(failures, cli.AgentFailure{Agent: agent, Err: fmt.Errorf("list installed powers for agent %q: %w", agent, listErr)})
			continue
		}
		agentPowers := filterAgentPowers(agent, flags, groups)
		if len(agentPowers) == 0 {
			agents = append(agents, cli.InstalledAgentSummary{Agent: agent, State: cli.InstalledAgentEmpty})
			continue
		}
		agents = append(agents, cli.InstalledAgentSummary{Agent: agent, State: cli.InstalledAgentListed})
		powers = append(powers, agentPowers...)
	}
	sortListedPowers(powers)
	result := cli.ListResult{Powers: powers, Agents: agents}
	if len(failures) != 0 {
		return result, &cli.PartialListError{Failures: failures}
	}
	return result, nil
}
