package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/domain"
)

func TestCodexPluginPrepareInstallCommands(t *testing.T) {
	definition := domain.PluginDefinition{
		ActualName:  "demo-plugin-v0.0.1",
		Marketplace: domain.MarketplaceDefinition{Name: "apm", Source: "/srv/apm-marketplace"},
	}
	tests := []struct {
		name      string
		list      string
		wantArgs  [][]string
		wantError error
	}{
		{
			name: "add missing marketplace",
			list: `{"marketplaces":[]}`,
			wantArgs: [][]string{
				{"plugin", "marketplace", "list", "--json"},
				{"plugin", "marketplace", "add", "/srv/apm-marketplace", "--json"},
			},
		},
		{
			name: "upgrade matching marketplace",
			list: `{"marketplaces":[{"name":"apm","marketplaceSource":{"sourceType":"local","source":"/srv/./apm-marketplace"}}]}`,
			wantArgs: [][]string{
				{"plugin", "marketplace", "list", "--json"},
				{"plugin", "marketplace", "upgrade", "apm", "--json"},
			},
		},
		{
			name:      "reject source conflict",
			list:      `{"marketplaces":[{"name":"apm","marketplaceSource":{"sourceType":"git","source":"owner/other"}}]}`,
			wantArgs:  [][]string{{"plugin", "marketplace", "list", "--json"}},
			wantError: ErrMarketplaceConflict,
		},
		{
			name:      "reject missing source fact",
			list:      `{"marketplaces":[{"name":"apm","marketplaceSource":{"sourceType":"git"}}]}`,
			wantArgs:  [][]string{{"plugin", "marketplace", "list", "--json"}},
			wantError: ErrPluginStateUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(test.list)}}
			adapter := newCodexPluginAdapter(runner)
			err := adapter.PrepareInstall(context.Background(), definition)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("PrepareInstall() error = %v, want %v", err, test.wantError)
			}
			assertPluginCommands(t, runner, "codex", test.wantArgs)
		})
	}
}

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

func TestCodexPluginInspectRejectsAmbiguousOrInvalidOutput(t *testing.T) {
	tests := []queuedMCPResponse{
		successfulMCPResponse(`{"installed":[{"name":"demo-plugin-v0.0.1","pluginId":"demo-plugin-v0.0.1@one"},{"name":"demo-plugin-v0.0.1","pluginId":"demo-plugin-v0.0.1@two"}]}`),
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

func TestCodexPluginMarketplaceComparesNonLocalSourceExactly(t *testing.T) {
	definition := domain.PluginDefinition{ActualName: "demo-plugin-v0.0.1", Marketplace: domain.MarketplaceDefinition{Name: "apm", Source: " owner/repo "}}
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(
		`{"marketplaces":[{"name":"apm","marketplaceSource":{"sourceType":"git","source":"owner/repo.git"}}]}`,
	)}}
	if err := newCodexPluginAdapter(runner).PrepareInstall(context.Background(), definition); !errors.Is(err, ErrMarketplaceConflict) {
		t.Fatalf("PrepareInstall() error = %v", err)
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
