package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestHermesPluginContract(t *testing.T) {
	root := filepath.Join(t.TempDir(), "hermes")
	t.Setenv("HERMES_HOME", root)
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`{"plugins":[{"name":"demo-plugin-v0.0.1","status":"disabled"}]}`),
		successfulMCPResponse(""),
		successfulMCPResponse(""),
	}}
	adapter := newHermesPluginAdapter(runner, configForAgent(t, domain.AgentHermes))
	if adapter.DeploymentMode() != domain.PluginDeploymentFilesystem {
		t.Fatalf("DeploymentMode() = %q", adapter.DeploymentMode())
	}
	fact, err := adapter.Inspect(context.Background(), "demo-plugin-v0.0.1")
	if err != nil || fact != (PluginFact{Presence: PluginPresent, ActualName: "demo-plugin-v0.0.1", Selector: "demo-plugin-v0.0.1"}) {
		t.Fatalf("Inspect() = %#v, %v", fact, err)
	}
	target := PluginTarget{ActualName: fact.ActualName, Selector: fact.Selector}
	if result, err := adapter.Add(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("Add() = %#v, %v", result, err)
	}
	if result, err := adapter.Remove(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("Remove() = %#v, %v", result, err)
	}
	path, err := adapter.TargetPath("demo-plugin-v0.0.1")
	if err != nil || path != filepath.Join(root, "plugins", "demo-plugin-v0.0.1") {
		t.Fatalf("TargetPath() = %q, %v", path, err)
	}
	assertPluginCommands(t, runner, "hermes", [][]string{
		{"plugins", "list", "--json"},
		{"plugins", "enable", "demo-plugin-v0.0.1"},
		{"plugins", "disable", "demo-plugin-v0.0.1"},
	})
}

func TestHermesPluginInspectIgnoresStatus(t *testing.T) {
	for _, status := range []string{"enabled", "disabled"} {
		runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(
			`[{"name":"demo-plugin-v0.0.1","status":"` + status + `"}]`,
		)}}
		fact, err := newHermesPluginAdapter(runner, configForAgent(t, domain.AgentHermes)).Inspect(context.Background(), "demo-plugin-v0.0.1")
		if err != nil || fact.Presence != PluginPresent {
			t.Fatalf("status %q Inspect() = %#v, %v", status, fact, err)
		}
	}
}

func TestHermesPluginAbsentAndUnsafeTarget(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(`{"plugins":[]}`)}}
	adapter := newHermesPluginAdapter(runner, configForAgent(t, domain.AgentHermes))
	fact, err := adapter.Inspect(context.Background(), "demo-plugin-v0.0.1")
	if err != nil || fact.Presence != PluginAbsent {
		t.Fatalf("Inspect() = %#v, %v", fact, err)
	}
	before := len(runner.commands)
	if _, err := adapter.TargetPath("../demo"); !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("TargetPath unsafe error = %v", err)
	}
	if len(runner.commands) != before {
		t.Fatal("unsafe TargetPath invoked CLI")
	}
}
