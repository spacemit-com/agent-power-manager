package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"apm/internal/domain"
)

type codexPluginAdapter struct {
	runner agentCLIRunner
}

func newCodexPluginAdapter(runner agentCLIRunner) *codexPluginAdapter {
	return &codexPluginAdapter{runner: runner}
}

func (adapter *codexPluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentMarketplace
}

// InspectMarketplace 只接受 Codex 返回的唯一 local 绝对 clean source；其他形态均视为状态不明。
func (adapter *codexPluginAdapter) InspectMarketplace(ctx context.Context, name string) (MarketplaceFact, error) {
	if !isSafeMarketplaceName(name) {
		return MarketplaceFact{}, fmt.Errorf("inspect marketplace name %q: %w", name, ErrInvalidPlugin)
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "marketplace", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentCodex, "marketplace list", result, runErr)
	}
	var document struct {
		Marketplaces []struct {
			Name              string `json:"name"`
			MarketplaceSource struct {
				SourceType string `json:"sourceType"`
				Source     string `json:"source"`
			} `json:"marketplaceSource"`
		} `json:"marketplaces"`
	}
	if err := decodePluginJSON(result.Stdout, &document); err != nil {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentCodex, "marketplace list", result, err)
	}
	if document.Marketplaces == nil {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentCodex, "marketplace list", result, errors.New("marketplace list has no marketplaces array"))
	}
	matches := make([]MarketplaceFact, 0, 1)
	for _, marketplace := range document.Marketplaces {
		if marketplace.Name != name {
			continue
		}
		if marketplace.MarketplaceSource.SourceType != "local" {
			return MarketplaceFact{}, pluginStateUnknown(domain.AgentCodex, "marketplace list", result, fmt.Errorf("marketplace %q has non-local source type %q", name, marketplace.MarketplaceSource.SourceType))
		}
		matches = append(matches, MarketplaceFact{Presence: MarketplacePresent, Name: name, Source: marketplace.MarketplaceSource.Source})
	}
	if len(matches) == 0 {
		fact := MarketplaceFact{Presence: MarketplaceAbsent, Name: name}
		return fact, validateMarketplaceFact(fact)
	}
	if len(matches) != 1 {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentCodex, "marketplace list", result, fmt.Errorf("marketplace %q appears %d times", name, len(matches)))
	}
	if err := validateMarketplaceFact(matches[0]); err != nil {
		return MarketplaceFact{}, pluginStateUnknown(domain.AgentCodex, "marketplace list", result, err)
	}
	return matches[0], nil
}

// RegisterMarketplace 注册调用方已校验的本地 marketplace，不追加 list 验证。
func (adapter *codexPluginAdapter) RegisterMarketplace(ctx context.Context, target MarketplaceTarget) (PluginMutationResult, error) {
	if err := validateMarketplaceTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "marketplace", "add", target.Source, "--json"}, "")
	return marketplaceMutationFromCommand(domain.AgentCodex, "add", target, result, runErr)
}

// UnregisterMarketplace 按固定名称删除注册；Source 仍需有效，以约束 transaction 目标完整性。
func (adapter *codexPluginAdapter) UnregisterMarketplace(ctx context.Context, target MarketplaceTarget) (PluginMutationResult, error) {
	if err := validateMarketplaceTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "marketplace", "remove", target.Name, "--json"}, "")
	return marketplaceMutationFromCommand(domain.AgentCodex, "remove", target, result, runErr)
}

func (adapter *codexPluginAdapter) Inspect(ctx context.Context, actualName string) (PluginFact, error) {
	if !isSafePluginActualName(actualName) {
		return PluginFact{}, fmt.Errorf("inspect plugin name %q: %w", actualName, ErrInvalidPlugin)
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return PluginFact{}, pluginStateUnknown(domain.AgentCodex, "plugin list", result, runErr)
	}
	var document struct {
		Installed []struct {
			Name     string `json:"name"`
			PluginID string `json:"pluginId"`
		} `json:"installed"`
	}
	if err := decodePluginJSON(result.Stdout, &document); err != nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentCodex, "plugin list", result, err)
	}
	if document.Installed == nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentCodex, "plugin list", result, errors.New("plugin list has no installed array"))
	}
	selectors := make([]string, 0, 1)
	for _, installed := range document.Installed {
		if installed.Name == actualName {
			selectors = append(selectors, installed.PluginID)
		}
	}
	if len(selectors) == 0 {
		fact := PluginFact{Presence: PluginAbsent, ActualName: actualName}
		return fact, validatePluginFact(fact)
	}
	if len(selectors) != 1 || strings.TrimSpace(selectors[0]) == "" {
		return PluginFact{}, pluginStateUnknown(domain.AgentCodex, "plugin list", result, fmt.Errorf("plugin %q has %d unambiguous selectors", actualName, len(selectors)))
	}
	fact := PluginFact{Presence: PluginPresent, ActualName: actualName, Selector: selectors[0]}
	if err := validatePluginFact(fact); err != nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentCodex, "plugin list", result, err)
	}
	return fact, nil
}

func (adapter *codexPluginAdapter) Add(ctx context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validatePluginTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "add", target.Selector, "--json"}, "")
	return pluginMutationFromCommand(domain.AgentCodex, "add", target, result, runErr, runErr == nil)
}

func (adapter *codexPluginAdapter) Remove(ctx context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validatePluginTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "remove", target.Selector, "--json"}, "")
	return pluginMutationFromCommand(domain.AgentCodex, "remove", target, result, runErr, runErr == nil)
}

func (adapter *codexPluginAdapter) TargetPath(string) (string, error) {
	return "", fmt.Errorf("codex marketplace plugin has no APM target path: %w", ErrUnsupportedAdapter)
}

var _ PluginAdapter = (*codexPluginAdapter)(nil)
var _ MarketplacePluginAdapter = (*codexPluginAdapter)(nil)
