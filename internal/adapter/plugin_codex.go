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

func (adapter *codexPluginAdapter) PrepareInstall(ctx context.Context, definition domain.PluginDefinition) error {
	if err := definition.Validate(); err != nil {
		return errors.Join(ErrInvalidPlugin, err)
	}
	result, runErr := adapter.runner.Run(ctx, "codex", []string{"plugin", "marketplace", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return pluginStateUnknown(domain.AgentCodex, "marketplace list", result, runErr)
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
		return pluginStateUnknown(domain.AgentCodex, "marketplace list", result, err)
	}
	matches := 0
	var currentSource, sourceType string
	for _, marketplace := range document.Marketplaces {
		if marketplace.Name == definition.Marketplace.Name {
			matches++
			currentSource = marketplace.MarketplaceSource.Source
			sourceType = marketplace.MarketplaceSource.SourceType
		}
	}
	if matches > 1 {
		return pluginStateUnknown(domain.AgentCodex, "marketplace list", result, fmt.Errorf("marketplace %q appears %d times", definition.Marketplace.Name, matches))
	}
	if matches == 0 {
		addResult, err := adapter.runner.Run(ctx, "codex", []string{"plugin", "marketplace", "add", definition.Marketplace.Source, "--json"}, "")
		return pluginPreparationCommandError(domain.AgentCodex, "add", addResult, err)
	}
	currentLocal := strings.EqualFold(strings.TrimSpace(sourceType), "local") || isLocalPluginMarketplaceSource(currentSource)
	expectedLocal := isLocalPluginMarketplaceSource(definition.Marketplace.Source)
	if strings.TrimSpace(currentSource) == "" {
		return pluginStateUnknown(domain.AgentCodex, "marketplace list", result, errors.New("matching marketplace has no source"))
	}
	current, err := normalizePluginMarketplaceSource(currentSource, currentLocal)
	if err != nil {
		return pluginStateUnknown(domain.AgentCodex, "marketplace list", result, err)
	}
	expected, err := normalizePluginMarketplaceSource(definition.Marketplace.Source, expectedLocal)
	if err != nil {
		return errors.Join(ErrInvalidPlugin, err)
	}
	if currentLocal != expectedLocal || current != expected {
		return fmt.Errorf("codex marketplace %q source %q differs from %q: %w", definition.Marketplace.Name, current, expected, ErrMarketplaceConflict)
	}
	upgradeResult, err := adapter.runner.Run(ctx, "codex", []string{"plugin", "marketplace", "upgrade", definition.Marketplace.Name, "--json"}, "")
	return pluginPreparationCommandError(domain.AgentCodex, "upgrade", upgradeResult, err)
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
