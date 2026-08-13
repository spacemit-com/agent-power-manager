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

func (adapter *claudePluginAdapter) PrepareInstall(ctx context.Context, definition domain.PluginDefinition) error {
	if err := definition.Validate(); err != nil {
		return errors.Join(ErrInvalidPlugin, err)
	}
	result, runErr := adapter.runner.Run(ctx, "claude", []string{"plugin", "marketplace", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, runErr)
	}
	var marketplaces []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		URL    string `json:"url"`
		Repo   string `json:"repo"`
	}
	if err := decodePluginJSON(result.Stdout, &marketplaces); err != nil {
		return pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, err)
	}
	matches := 0
	var currentSource string
	for _, marketplace := range marketplaces {
		if marketplace.Name != definition.Marketplace.Name {
			continue
		}
		matches++
		for _, candidate := range []string{marketplace.URL, marketplace.Repo, marketplace.Source} {
			if strings.TrimSpace(candidate) != "" && !isClaudeMarketplaceSourceKind(candidate) {
				currentSource = candidate
				break
			}
		}
	}
	if matches > 1 {
		return pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, fmt.Errorf("marketplace %q appears %d times", definition.Marketplace.Name, matches))
	}
	if matches == 0 {
		addResult, err := adapter.runner.Run(ctx, "claude", []string{"plugin", "marketplace", "add", definition.Marketplace.Source, "--scope", "user"}, "")
		return pluginPreparationCommandError(domain.AgentClaudeCode, "add", addResult, err)
	}
	if strings.TrimSpace(currentSource) == "" {
		return pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, errors.New("matching marketplace has no source"))
	}
	currentLocal := isLocalPluginMarketplaceSource(currentSource)
	expectedLocal := isLocalPluginMarketplaceSource(definition.Marketplace.Source)
	current, err := normalizePluginMarketplaceSource(currentSource, currentLocal)
	if err != nil {
		return pluginStateUnknown(domain.AgentClaudeCode, "marketplace list", result, err)
	}
	expected, err := normalizePluginMarketplaceSource(definition.Marketplace.Source, expectedLocal)
	if err != nil {
		return errors.Join(ErrInvalidPlugin, err)
	}
	if currentLocal != expectedLocal || current != expected {
		return fmt.Errorf("claude marketplace %q source %q differs from %q: %w", definition.Marketplace.Name, current, expected, ErrMarketplaceConflict)
	}
	updateResult, err := adapter.runner.Run(ctx, "claude", []string{"plugin", "marketplace", "update", definition.Marketplace.Name}, "")
	return pluginPreparationCommandError(domain.AgentClaudeCode, "update", updateResult, err)
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
	selectors := make([]string, 0, 1)
	for _, plugin := range installed {
		name, _, found := strings.Cut(plugin.ID, "@")
		if found && name == actualName {
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
