package adapter

import (
	"context"
	"errors"
	"testing"

	"apm/internal/domain"
)

func TestClaudePluginPrepareInstallCommands(t *testing.T) {
	definition := domain.PluginDefinition{
		ActualName:  "demo-plugin-v0.0.1",
		Marketplace: domain.MarketplaceDefinition{Name: "apm", Source: "https://example.com/apm.git"},
	}
	tests := []struct {
		name      string
		list      string
		wantArgs  [][]string
		wantError error
	}{
		{
			name: "add missing marketplace",
			list: `[]`,
			wantArgs: [][]string{
				{"plugin", "marketplace", "list", "--json"},
				{"plugin", "marketplace", "add", "https://example.com/apm.git", "--scope", "user"},
			},
		},
		{
			name: "update matching marketplace",
			list: `[{"name":"apm","source":"git","url":"https://example.com/apm.git"}]`,
			wantArgs: [][]string{
				{"plugin", "marketplace", "list", "--json"},
				{"plugin", "marketplace", "update", "apm"},
			},
		},
		{
			name:      "reject source conflict",
			list:      `[{"name":"apm","source":"git","url":"https://example.com/other.git"}]`,
			wantArgs:  [][]string{{"plugin", "marketplace", "list", "--json"}},
			wantError: ErrMarketplaceConflict,
		},
		{
			name:      "reject missing source fact",
			list:      `[{"name":"apm","source":"git"}]`,
			wantArgs:  [][]string{{"plugin", "marketplace", "list", "--json"}},
			wantError: ErrPluginStateUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(test.list)}}
			err := newClaudePluginAdapter(runner).PrepareInstall(context.Background(), definition)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("PrepareInstall() error = %v, want %v", err, test.wantError)
			}
			assertPluginCommands(t, runner, "claude", test.wantArgs)
		})
	}
}

func TestClaudePluginInspectAndMutations(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`[{"id":"demo-plugin-v0.0.1@apm","scope":"user","enabled":false}]`),
		successfulMCPResponse(""),
		successfulMCPResponse(""),
	}}
	adapter := newClaudePluginAdapter(runner)
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
	assertPluginCommands(t, runner, "claude", [][]string{
		{"plugin", "list", "--json"},
		{"plugin", "install", "demo-plugin-v0.0.1@apm", "--scope", "user"},
		{"plugin", "uninstall", "demo-plugin-v0.0.1@apm", "--scope", "user"},
	})
}

func TestClaudePluginInspectAbsentAndAmbiguous(t *testing.T) {
	absentRunner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(`[]`)}}
	fact, err := newClaudePluginAdapter(absentRunner).Inspect(context.Background(), "demo-plugin-v0.0.1")
	if err != nil || fact != (PluginFact{Presence: PluginAbsent, ActualName: "demo-plugin-v0.0.1"}) {
		t.Fatalf("absent Inspect() = %#v, %v", fact, err)
	}

	ambiguousRunner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(
		`[{"id":"demo-plugin-v0.0.1@one"},{"id":"demo-plugin-v0.0.1@two"}]`,
	)}}
	if _, err := newClaudePluginAdapter(ambiguousRunner).Inspect(context.Background(), "demo-plugin-v0.0.1"); !errors.Is(err, ErrPluginStateUnknown) {
		t.Fatalf("ambiguous Inspect() error = %v", err)
	}
}
