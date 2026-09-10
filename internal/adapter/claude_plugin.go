package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"apm/internal/domain"
)

type claudePluginAdapter struct {
	runner agentCLIRunner
}

func newClaudePluginAdapter(runner agentCLIRunner) *claudePluginAdapter {
	return &claudePluginAdapter{runner: runner}
}

func (adapter *claudePluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}

// InspectMarketplace 只接受 Claude directory/local 类型中唯一的 path/installLocation 本地绝对路径。
func (adapter *claudePluginAdapter) InspectMarketplace(ctx context.Context, name string) (MarketplaceFact, error) {
	if !isSafeMarketplaceName(name) {
		return MarketplaceFact{}, fmt.Errorf("inspect marketplace name %q: %w", name, ErrInvalidPlugin)
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "marketplace", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, runErr)
	}
	var marketplaces []struct {
		Name            string `json:"name"`
		Source          string `json:"source"`
		Path            string `json:"path"`
		InstallLocation string `json:"installLocation"`
	}
	if err := decodePluginJSON(result.Stdout, &marketplaces); err != nil {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, err)
	}
	if marketplaces == nil {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, errors.New("marketplace list is null"))
	}
	matches := make([]MarketplaceFact, 0, 1)
	for _, marketplace := range marketplaces {
		if marketplace.Name != name {
			continue
		}
		if marketplace.Source != "directory" && marketplace.Source != "local" {
			return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, fmt.Errorf("marketplace %q has non-local source type %q", name, marketplace.Source))
		}
		// 真实 CLI 同时返回相等的 path 与 installLocation；去重后必须唯一。
		candidates := make([]string, 0, 2)
		seen := make(map[string]struct{}, 2)
		for _, candidate := range []string{marketplace.Path, marketplace.InstallLocation} {
			trimmed := strings.TrimSpace(candidate)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			candidates = append(candidates, trimmed)
		}
		if len(candidates) != 1 {
			return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, fmt.Errorf("marketplace %q has %d local source fields", name, len(candidates)))
		}
		matches = append(matches, MarketplaceFact{Presence: MarketplacePresent, Name: name, Source: candidates[0]})
	}
	if len(matches) == 0 {
		fact := MarketplaceFact{Presence: MarketplaceAbsent, Name: name}
		return fact, validateMarketplaceFact(fact)
	}
	if len(matches) != 1 {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, fmt.Errorf("marketplace %q appears %d times", name, len(matches)))
	}
	if err := validateMarketplaceFact(matches[0]); err != nil {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, err)
	}
	return matches[0], nil
}

// RegisterMarketplace 注册调用方已校验的本地 marketplace，不追加 list 验证。
func (adapter *claudePluginAdapter) RegisterMarketplace(ctx context.Context, target MarketplaceTarget) (PluginMutationResult, error) {
	if err := validateMarketplaceTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "marketplace", "add", target.Source, "--scope", "user"}, "")
	return marketplaceMutationFromCommand(domain.AgentClaudeCode, "add", target, result, runErr)
}

// UnregisterMarketplace 按固定名称删除 user scope 注册，不追加 list 验证。
func (adapter *claudePluginAdapter) UnregisterMarketplace(ctx context.Context, target MarketplaceTarget) (PluginMutationResult, error) {
	if err := validateMarketplaceTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "marketplace", "remove", target.Name, "--scope", "user"}, "")
	return marketplaceMutationFromCommand(domain.AgentClaudeCode, "remove", target, result, runErr)
}

func isClaudeMarketplaceSourceKind(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "git", "github", "local", "directory":
		return true
	default:
		return false
	}
}

func (adapter *claudePluginAdapter) Inspect(ctx context.Context, actualName string) (PluginFact, error) {
	if !isSafePluginActualName(actualName) {
		return PluginFact{}, fmt.Errorf("inspect plugin name %q: %w", actualName, ErrInvalidPlugin)
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return PluginFact{}, pluginStateUnknown(domain.AgentClaudeCode, "plugin list", result, runErr)
	}
	var installed []struct {
		ID string `json:"id"`
	}
	if err := decodePluginJSON(result.Stdout, &installed); err != nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentClaudeCode, "plugin list", result, err)
	}
	if installed == nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentClaudeCode, "plugin list", result, errors.New("plugin list is null"))
	}
	selectors := make([]string, 0, 1)
	for _, plugin := range installed {
		name, _, found := strings.Cut(plugin.ID, "@")
		if plugin.ID == actualName || found && name == actualName {
			selectors = append(selectors, plugin.ID)
		}
	}
	if len(selectors) == 0 {
		fact := PluginFact{Presence: PluginAbsent, ActualName: actualName}
		return fact, validatePluginFact(fact)
	}
	if len(selectors) != 1 {
		return PluginFact{}, pluginStateUnknown(domain.AgentClaudeCode, "plugin list", result, fmt.Errorf("plugin %q has %d selectors", actualName, len(selectors)))
	}
	fact := PluginFact{Presence: PluginPresent, ActualName: actualName, Selector: selectors[0]}
	if err := validatePluginFact(fact); err != nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentClaudeCode, "plugin list", result, err)
	}
	return fact, nil
}

func (adapter *claudePluginAdapter) Add(ctx context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validatePluginTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "install", target.Selector, "--scope", "user"}, "")
	return pluginMutationFromCommand(domain.AgentClaudeCode, "install", target, result, runErr, runErr == nil)
}

func (adapter *claudePluginAdapter) Remove(ctx context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validatePluginTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "uninstall", target.Selector, "--scope", "user"}, "")
	return pluginMutationFromCommand(domain.AgentClaudeCode, "uninstall", target, result, runErr, runErr == nil)
}

func (adapter *claudePluginAdapter) TargetPath(string) (string, error) {
	return "", fmt.Errorf("claude marketplace plugin has no APM target path: %w", ErrUnsupportedAdapter)
}

var _ PluginAdapter = (*claudePluginAdapter)(nil)
var _ MarketplacePluginAdapter = (*claudePluginAdapter)(nil)
