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

func installedListAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	if flags.Type == string(domain.PowerTypeAgent) && flags.Agent != "" && domain.AgentID(flags.Agent) != domain.AgentBibit {
		return nil, fmt.Errorf("list installed power type %q only supports agent %q", domain.PowerTypeAgent, domain.AgentBibit)
	}
	target := domain.AgentID(flags.Agent)
	if flags.Type == string(domain.PowerTypeAgent) && flags.Agent == "" {
		target = domain.AgentBibit
	}
	if target == domain.AllAgents {
		return snapshot.Ordered(), nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("list installed powers for agent %q: unsupported or not installed", target)
	}
	return []domain.AgentID{target}, nil
}

// listInstalled 无锁读取 Agent 快照和配置，生成 best-effort 的已安装清单。
// 结果不是跨 Agent、manifest 与目标状态的全局一致快照；读取失败按单个或全部 Agent 语义返回。
func (s *ListService) listInstalled(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
	snapshot, err := s.registry.RefreshInstalled()
	if err != nil {
		return cli.ListResult{}, fmt.Errorf("refresh installed agents for list: %w", err)
	}
	agents, err := installedListAgents(flags, snapshot)
	if err != nil {
		return cli.ListResult{}, err
	}

	inputs := make([]agentPowerGroups, 0, len(agents))
	failures := make([]cli.AgentFailure, 0)
	for _, agent := range agents {
		groups, listErr := s.config.List(agent)
		if listErr != nil {
			wrapped := fmt.Errorf("list installed powers for agent %q: %w", agent, listErr)
			if domain.AgentID(flags.Agent) != domain.AllAgents {
				return cli.ListResult{}, wrapped
			}
			failures = append(failures, cli.AgentFailure{Agent: agent, Err: wrapped})
			continue
		}
		inputs = append(inputs, agentPowerGroups{agent: agent, groups: groups})
	}

	result := makeInstalledListResult(flags, inputs)
	if len(failures) != 0 {
		return result, &cli.PartialListError{Failures: failures}
	}
	return result, nil
}
