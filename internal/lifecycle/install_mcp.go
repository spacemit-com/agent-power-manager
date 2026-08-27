package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/catalog"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"

	"github.com/Masterminds/semver/v3"
)

// installMCP 先核对本地清单与目标 Agent CLI 现状，按共享状态策略决策后准备定义并交由 MCP 事务发布。
func (s *InstallService) installMCP(ctx context.Context, agent domain.AgentID, entry domain.CatalogEntry, snapshot catalog.Snapshot) (cli.InstallItem, error) {
	ref := entry.Ref
	previous, installed, err := s.config.Find(agent, domain.PowerTypeMCP, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed MCP %+v for agent %q: %w", ref, agent, err)
	}

	manifest := manifestAbsent
	if installed && previous.Ref.Version == ref.Version {
		manifest = manifestSameVersion
	} else if installed {
		manifest = manifestDifferentVersion
	}
	if manifest == manifestSameVersion {
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeUnchanged}, nil
	}

	mcpAdapter, err := s.registry.ResolveMCP(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve MCP install adapter for agent %q: %w", agent, err)
	}
	presence, err := mcpAdapter.Inspect(ctx, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("inspect MCP %+v for agent %q: %w", ref, agent, err)
	}
	target := targetAbsent
	if presence == adapter.MCPPresent {
		target = targetComplete
	}

	decision := decideInstallState(manifest, target)
	switch decision.action {
	case actionInstallNew:
		server, err := s.cloud.GetMCP(ctx, entry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get MCP %+v from Cloud: %w", ref, err)
		}
		request := transaction.MCPInstallRequest{
			Agent:       agent,
			Power:       ref,
			Description: entry.Description,
			Tags:        entry.Tags,
			Server:      mcpServerPointer(server),
			Adapter:     mcpAdapter,
		}
		if err := s.transactions.InstallMCP(ctx, request); err != nil {
			return cli.InstallItem{}, fmt.Errorf("install MCP %+v for agent %q: %w", ref, agent, err)
		}
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeInstalled}, nil
	case actionReplace:
		previousEntry, err := snapshot.Metadata(previous.Ref)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get previous MCP catalog entry %+v: %w", previous.Ref, err)
		}
		previousServer, err := s.cloud.GetMCP(ctx, previousEntry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get previous MCP %+v from Cloud: %w", previous.Ref, err)
		}
		server, err := s.cloud.GetMCP(ctx, entry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get target MCP %+v from Cloud: %w", ref, err)
		}
		previousCopy := previous.Ref
		request := transaction.MCPInstallRequest{
			Agent:          agent,
			Power:          ref,
			Description:    entry.Description,
			Tags:           entry.Tags,
			Previous:       &previousCopy,
			Server:         mcpServerPointer(server),
			PreviousServer: mcpServerPointer(previousServer),
			Adapter:        mcpAdapter,
		}
		if err := s.transactions.InstallMCP(ctx, request); err != nil {
			return cli.InstallItem{}, fmt.Errorf("install MCP %+v for agent %q: %w", ref, agent, err)
		}
		outcome := cli.InstallOutcomeInstalled
		if semver.MustParse(ref.Version).GreaterThan(semver.MustParse(previous.Ref.Version)) {
			outcome = cli.InstallOutcomeUpgraded
		} else {
			outcome = cli.InstallOutcomeDowngraded
		}
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}, nil
	case actionCleanAndInstall:
		if decision.warnMissing {
			logInstallMissingTarget(agent, previous.Ref)
		}
		if _, err := s.transactions.UninstallMCP(ctx, transaction.MCPUninstallRequest{
			Agent:   agent,
			Power:   previous.Ref,
			Adapter: mcpAdapter,
		}); err != nil {
			return cli.InstallItem{}, fmt.Errorf("repair installed MCP %+v for agent %q: %w", previous.Ref, agent, err)
		}
		server, err := s.cloud.GetMCP(ctx, entry)
		if err != nil {
			return cli.InstallItem{}, fmt.Errorf("get MCP %+v from Cloud: %w", ref, err)
		}
		request := transaction.MCPInstallRequest{
			Agent:       agent,
			Power:       ref,
			Description: entry.Description,
			Tags:        entry.Tags,
			Server:      mcpServerPointer(server),
			Adapter:     mcpAdapter,
		}
		if err := s.transactions.InstallMCP(ctx, request); err != nil {
			return cli.InstallItem{}, fmt.Errorf("install MCP %+v for agent %q: %w", ref, agent, err)
		}
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeInstalled}, nil
	case actionRejectConflict:
		return cli.InstallItem{}, fmt.Errorf("unmanaged MCP %q exists for agent %q: %w", ref.Name, agent, adapter.ErrTargetConflict)
	default:
		return cli.InstallItem{}, fmt.Errorf("install MCP %+v for agent %q: unresolvable entry state", ref, agent)
	}
}

func mcpServerPointer(server domain.MCPServer) *domain.MCPServer {
	clone := server.Clone()
	return &clone
}
