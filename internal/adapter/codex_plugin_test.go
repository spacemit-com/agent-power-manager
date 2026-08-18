package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCodexPluginInspectAndMutations(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`{"installed":[{"name":"demo-plugin-v0.0.1","pluginId":"demo-plugin-v0.0.1@apm","enabled":false}]}`),
		successfulMCPResponse(""),
		successfulMCPResponse(""),
	}}
	adapter := newCodexPluginAdapter(runner)
	fact, err := adapter.Inspect(context.Background(), "demo-plugin-v0.0.1")
	if err != nil || fact != (PluginFact{Presence: PluginPresent, ActualName: "demo-plugin-v0.0.1", Selector: "demo-plugin-v0.0.1@apm"}) {
		t.Fatalf("Inspect() = %#v, %v", fact, err)
	}
	target := PluginTarget{ActualName: fact.ActualName, Selector: fact.Selector}
	if result, err := adapter.Add(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("Add() = %#v, %v", result, err)
	}
	if result, err := adapter.Remove(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("Remove() = %#v, %v", result, err)
	}
	assertPluginCommands(t, runner, "codex", [][]string{
		{"plugin", "list", "--json"},
		{"plugin", "add", "demo-plugin-v0.0.1@apm", "--json"},
		{"plugin", "remove", "demo-plugin-v0.0.1@apm", "--json"},
	})
}

func TestCodexPluginMarketplaceInspectCommandsAndFacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache", "plugins", "codex", "demo", "v0.1.0")
	tests := []struct {
		name string
		list string
		want MarketplaceFact
	}{
		{
			name: "absent",
			list: `{"marketplaces":[]}`,
			want: MarketplaceFact{Presence: MarketplaceAbsent, Name: "apm-demo"},
		},
		{
			name: "present local source",
			list: `{"marketplaces":[{"name":"apm-demo","marketplaceSource":{"sourceType":"local","source":"` + root + `"}}]}`,
			want: MarketplaceFact{Presence: MarketplacePresent, Name: "apm-demo", Source: root},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(test.list)}}
			fact, err := newCodexPluginAdapter(runner).InspectMarketplace(context.Background(), "apm-demo")
			if err != nil || fact != test.want {
				t.Fatalf("InspectMarketplace() = %#v, %v; want %#v", fact, err, test.want)
			}
			assertPluginCommands(t, runner, "codex", [][]string{{"plugin", "marketplace", "list", "--json"}})
		})
	}
}

func TestCodexPluginMarketplaceInspectRejectsAmbiguousInvalidOrTruncatedOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache", "plugins", "codex", "demo", "v0.1.0")
	tests := []queuedMCPResponse{
		successfulMCPResponse(`{"marketplaces":[{"name":"apm-demo","marketplaceSource":{"sourceType":"local","source":"` + root + `"}},{"name":"apm-demo","marketplaceSource":{"sourceType":"local","source":"` + root + `"}}]}`),
		successfulMCPResponse(`{"marketplaces":[{"name":"apm-demo","marketplaceSource":{"sourceType":"git","source":"owner/repo"}}]}`),
		successfulMCPResponse(`{"marketplaces":[{"name":"apm-demo","marketplaceSource":{"sourceType":"local","source":"relative/cache"}}]}`),
		successfulMCPResponse(`{"marketplaces":[{"name":"apm-demo","marketplaceSource":{"sourceType":"local"}}]}`),
		successfulMCPResponse(`{}`),
		successfulMCPResponse(`{"marketplaces":null}`),
		successfulMCPResponse(`{"marketplaces":[]} {}`),
		truncatedMCPResponse(`{"marketplaces":[]}`),
		failedMCPResponse("failed"),
	}
	for index, response := range tests {
		runner := &recordingMCPRunner{responses: []queuedMCPResponse{response}}
		_, err := newCodexPluginAdapter(runner).InspectMarketplace(context.Background(), "apm-demo")
		if !errors.Is(err, ErrPluginStateUnknown) {
			t.Fatalf("case %d error = %v, want ErrPluginStateUnknown", index, err)
		}
	}
}

func TestCodexPluginMarketplaceMutationCommands(t *testing.T) {
	source := "/abs/cache/plugins/codex/demo/v0.1.0"
	target := MarketplaceTarget{Name: "apm-demo", Source: source}
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(""),
		successfulMCPResponse(""),
	}}
	adapter := newCodexPluginAdapter(runner)
	if result, err := adapter.RegisterMarketplace(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("RegisterMarketplace() = %#v, %v", result, err)
	}
	if result, err := adapter.UnregisterMarketplace(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("UnregisterMarketplace() = %#v, %v", result, err)
	}
	assertPluginCommands(t, runner, "codex", [][]string{
		{"plugin", "marketplace", "add", source, "--json"},
		{"plugin", "marketplace", "remove", "apm-demo", "--json"},
	})
}

func TestCodexPluginMarketplaceMutationsClassifyRunnerFailures(t *testing.T) {
	target := MarketplaceTarget{Name: "apm-demo", Source: "/abs/cache/plugins/codex/demo/v0.1.0"}
	tests := []struct {
		name      string
		response  queuedMCPResponse
		wantState PluginMutationState
		wantErr   error
	}{
		{name: "not started", response: queuedMCPResponse{err: ErrAgentCLIUnavailable}, wantState: PluginMutationNotApplied, wantErr: ErrAgentCLIUnavailable},
		{name: "failed after start", response: failedMCPResponse("failed"), wantState: PluginMutationUnknown, wantErr: ErrPluginMutationUnknown},
		{name: "truncated after start", response: truncatedMCPResponse("{}"), wantState: PluginMutationUnknown, wantErr: ErrPluginMutationUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{test.response}}
			result, err := newCodexPluginAdapter(runner).RegisterMarketplace(context.Background(), target)
			if result.State != test.wantState || !errors.Is(err, test.wantErr) {
				t.Fatalf("RegisterMarketplace() = %#v, %v; want %q, %v", result, err, test.wantState, test.wantErr)
			}
		})
	}
}

func TestCodexPluginInspectRejectsAmbiguousOrInvalidOutput(t *testing.T) {
	tests := []queuedMCPResponse{
		successfulMCPResponse(`{"installed":[{"name":"demo-plugin-v0.0.1","pluginId":"demo-plugin-v0.0.1@one"},{"name":"demo-plugin-v0.0.1","pluginId":"demo-plugin-v0.0.1@two"}]}`),
		successfulMCPResponse(`{}`),
		successfulMCPResponse(`{"installed":null}`),
		successfulMCPResponse(`null`),
		successfulMCPResponse(`{"installed":[]} {}`),
		truncatedMCPResponse(`{"installed":[]}`),
		failedMCPResponse("failed"),
	}
	for index, response := range tests {
		runner := &recordingMCPRunner{responses: []queuedMCPResponse{response}}
		_, err := newCodexPluginAdapter(runner).Inspect(context.Background(), "demo-plugin-v0.0.1")
		if !errors.Is(err, ErrPluginStateUnknown) {
			t.Fatalf("case %d error = %v, want ErrPluginStateUnknown", index, err)
		}
	}
}

func TestNormalizePluginMarketplaceSource(t *testing.T) {
	local, err := normalizePluginMarketplaceSource(filepath.Join(".", "marketplace"), true)
	if err != nil || !filepath.IsAbs(local) || filepath.Base(local) != "marketplace" {
		t.Fatalf("normalize local = %q, %v", local, err)
	}
	remote, err := normalizePluginMarketplaceSource(" owner/repo ", false)
	if err != nil || remote != "owner/repo" {
		t.Fatalf("normalize remote = %q, %v", remote, err)
	}
}

func assertPluginCommands(t *testing.T, runner *recordingMCPRunner, executable string, want [][]string) {
	t.Helper()
	if len(runner.commands) != len(want) {
		t.Fatalf("commands = %#v, want %d calls", runner.commands, len(want))
	}
	for index, args := range want {
		command := runner.commands[index]
		if command.executable != executable || !reflect.DeepEqual(command.args, args) || command.stdin != "" {
			t.Fatalf("command %d = %#v, want %s %#v", index, command, executable, args)
		}
	}
}
