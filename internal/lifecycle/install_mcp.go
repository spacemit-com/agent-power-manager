package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// installMCP 根据 manifest 与目标 Agent CLI 的联合状态决定是否读取 Cloud 定义并启动事务。
func (s *InstallService) installMCP(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	mcpAdapter, err := s.registry.ResolveMCP(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve MCP install adapter for agent %q: %w", agent, err)
	}
	previous, installed, err := s.config.Find(agent, domain.PowerTypeMCP, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed MCP %+v for agent %q: %w", ref, agent, err)
	}
	presence, err := mcpAdapter.Inspect(ctx, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect MCP %+v for agent %q: %w", ref, agent, err)
	}
	if !installed && presence == adapter.MCPPresent {
		return cli.InstallItem{}, fmt.Errorf("unmanaged MCP %q exists for agent %q: %w", ref.Name, agent, errors.Join(adapter.ErrMCPConflict, adapter.ErrTargetConflict))
	}
	if installed && previous.Version == ref.Version && presence == adapter.MCPPresent {
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeUnchanged}, nil
	}

	request := transaction.MCPInstallRequest{Agent: agent, Power: ref, Adapter: mcpAdapter}
	if installed {
		previousCopy := previous
		request.Previous = &previousCopy
	}
	if presence == adapter.MCPPresent && installed && previous.Version != ref.Version {
		previousServer, err := s.cloud.GetMCP(previous)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get previous MCP %+v from Cloud: %w", previous, err)
		}
		server, err := s.cloud.GetMCP(ref)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get target MCP %+v from Cloud: %w", ref, err)
		}
		request.PreviousServer = mcpServerPointer(previousServer)
		request.Server = mcpServerPointer(server)
	} else if presence == adapter.MCPAbsent && !installed {
		server, err := s.cloud.GetMCP(ref)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get MCP %+v from Cloud: %w", ref, err)
		}
		request.Server = mcpServerPointer(server)
	}
	if err := s.transactions.InstallMCP(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install MCP %+v for agent %q: %w", ref, agent, err)
	}

	outcome := cli.InstallOutcomeInstalled
	if installed {
		if semver.MustParse(ref.Version).GreaterThan(semver.MustParse(previous.Version)) {
			outcome = cli.InstallOutcomeUpgraded
		} else if previous.Version != ref.Version {
			outcome = cli.InstallOutcomeDowngraded
		} else {
			outcome = cli.InstallOutcomeUnchanged
		}
	}
	return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}, nil
}

func mcpServerPointer(server domain.MCPServer) *domain.MCPServer {
	clone := server.Clone()
	return &clone
}
