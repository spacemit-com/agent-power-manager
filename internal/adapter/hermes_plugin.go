package adapter

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"apm/internal/domain"
)

type hermesPluginAdapter struct {
	runner  agentCLIRunner
	profile agentProfile
}

func newHermesPluginAdapter(runner agentCLIRunner, profile agentProfile) *hermesPluginAdapter {
	return &hermesPluginAdapter{runner: runner, profile: profile}
}

func (adapter *hermesPluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentFilesystem
}

func (adapter *hermesPluginAdapter) PrepareInstall(context.Context, domain.PluginDefinition) error {
	return nil
}

func (adapter *hermesPluginAdapter) Inspect(ctx context.Context, actualName string) (PluginFact, error) {
	if !isSafePluginActualName(actualName) {
		return PluginFact{}, fmt.Errorf("inspect plugin name %q: %w", actualName, ErrInvalidPlugin)
	}
	result, runErr := adapter.runner.Run(ctx, "hermes", []string{"plugins", "list", "--json"}, "")
	if runErr != nil || result.StdoutTruncated || result.StderrTruncated {
		return PluginFact{}, pluginStateUnknown(domain.AgentHermes, "plugin list", result, runErr)
	}
	plugins, err := decodeHermesPluginList(result.Stdout)
	if err != nil {
		return PluginFact{}, pluginStateUnknown(domain.AgentHermes, "plugin list", result, err)
	}
	matches := 0
	for _, plugin := range plugins {
		if plugin.Name == actualName {
			matches++
		}
	}
	if matches == 0 {
		fact := PluginFact{Presence: PluginAbsent, ActualName: actualName}
		return fact, validatePluginFact(fact)
	}
	if matches != 1 {
		return PluginFact{}, pluginStateUnknown(domain.AgentHermes, "plugin list", result, fmt.Errorf("plugin %q appears %d times", actualName, matches))
	}
	fact := PluginFact{Presence: PluginPresent, ActualName: actualName, Selector: actualName}
	return fact, validatePluginFact(fact)
}

type hermesPluginListEntry struct {
	Name string `json:"name"`
}

func decodeHermesPluginList(document string) ([]hermesPluginListEntry, error) {
	trimmed := strings.TrimSpace(document)
	if strings.HasPrefix(trimmed, "[") {
		var plugins []hermesPluginListEntry
		if err := decodePluginJSON(document, &plugins); err != nil {
			return nil, err
		}
		return plugins, nil
	}
	var envelope struct {
		Plugins []hermesPluginListEntry `json:"plugins"`
	}
	if err := decodePluginJSON(document, &envelope); err != nil {
		return nil, err
	}
	if envelope.Plugins == nil && !bytes.Contains([]byte(document), []byte(`"plugins"`)) {
		return nil, fmt.Errorf("Hermes plugin list has no plugins field")
	}
	return envelope.Plugins, nil
}

func (adapter *hermesPluginAdapter) Add(ctx context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validatePluginTarget(target); err != nil || target.Selector != target.ActualName {
		if err == nil {
			err = fmt.Errorf("Hermes selector %q differs from actual name %q: %w", target.Selector, target.ActualName, ErrInvalidPlugin)
		}
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "hermes", []string{"plugins", "enable", target.ActualName}, "")
	return pluginMutationFromCommand(domain.AgentHermes, "enable", target, result, runErr, runErr == nil)
}

func (adapter *hermesPluginAdapter) Remove(ctx context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validatePluginTarget(target); err != nil || target.Selector != target.ActualName {
		if err == nil {
			err = fmt.Errorf("Hermes selector %q differs from actual name %q: %w", target.Selector, target.ActualName, ErrInvalidPlugin)
		}
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	result, runErr := adapter.runner.Run(ctx, "hermes", []string{"plugins", "disable", target.ActualName}, "")
	return pluginMutationFromCommand(domain.AgentHermes, "disable", target, result, runErr, runErr == nil)
}

func (adapter *hermesPluginAdapter) TargetPath(actualName string) (string, error) {
	return adapter.profile.pluginTargetPath(actualName)
}

var _ PluginAdapter = (*hermesPluginAdapter)(nil)
