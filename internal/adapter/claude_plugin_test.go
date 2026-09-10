package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestClaudePluginInspectAndMutations(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(`[{"id":"demo-plugin@apm-demo-plugin","scope":"user","enabled":false}]`),
		successfulMCPResponse(""),
		successfulMCPResponse(""),
	}}
	adapter := newClaudePluginAdapter(runner)
	fact, err := adapter.Inspect(context.Background(), "demo-plugin")
	if err != nil || fact != (PluginFact{Presence: PluginPresent, ActualName: "demo-plugin", Selector: "demo-plugin@apm-demo-plugin"}) {
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
		{"plugin", "install", "demo-plugin@apm-demo-plugin", "--scope", "user"},
		{"plugin", "uninstall", "demo-plugin@apm-demo-plugin", "--scope", "user"},
	})
}

func TestClaudePluginMarketplaceInspectCommandsAndFacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache", "plugins", "claude-code", "demo", "v0.1.0")
	tests := []struct {
		name string
		list string
		want MarketplaceFact
	}{
		{
			name: "absent",
			list: `[]`,
			want: MarketplaceFact{Presence: MarketplaceAbsent, Name: "apm-demo"},
		},
		{
			name: "present directory path",
			list: `[{"name":"apm-demo","source":"directory","path":"` + root + `"}]`,
			want: MarketplaceFact{Presence: MarketplacePresent, Name: "apm-demo", Source: root},
		},
		{
			name: "present local installLocation",
			list: `[{"name":"apm-demo","source":"local","installLocation":"` + root + `"}]`,
			want: MarketplaceFact{Presence: MarketplacePresent, Name: "apm-demo", Source: root},
		},
		{
			name: "present identical path and installLocation",
			list: `[{"name":"apm-demo","source":"directory","path":"` + root + `","installLocation":"` + root + `"}]`,
			want: MarketplaceFact{Presence: MarketplacePresent, Name: "apm-demo", Source: root},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(test.list)}}
			fact, err := newClaudePluginAdapter(runner).InspectMarketplace(context.Background(), "apm-demo")
			if err != nil || fact != test.want {
				t.Fatalf("InspectMarketplace() = %#v, %v; want %#v", fact, err, test.want)
			}
			assertPluginCommands(t, runner, "claude", [][]string{{"plugin", "marketplace", "list", "--json"}})
		})
	}
}

func TestClaudePluginMarketplaceInspectRejectsAmbiguousInvalidOrTruncatedOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache", "plugins", "claude-code", "demo", "v0.1.0")
	tests := []queuedMCPResponse{
		successfulMCPResponse(`[{"name":"apm-demo","source":"directory","path":"` + root + `"},{"name":"apm-demo","source":"directory","path":"` + root + `"}]`),
		successfulMCPResponse(`[{"name":"apm-demo","source":"github","repo":"owner/repo"}]`),
		successfulMCPResponse(`[{"name":"apm-demo","source":"directory","path":"relative/cache"}]`),
		successfulMCPResponse(`[{"name":"apm-demo","source":"directory"}]`),
		successfulMCPResponse(`[{"name":"apm-demo","source":"directory","path":"` + root + `","installLocation":"` + filepath.Join(root, "other") + `"}]`),
		successfulMCPResponse(`null`),
		successfulMCPResponse(`[] {}`),
		truncatedMCPResponse(`[]`),
		failedMCPResponse("failed"),
	}
	for index, response := range tests {
		runner := &recordingMCPRunner{responses: []queuedMCPResponse{response}}
		_, err := newClaudePluginAdapter(runner).InspectMarketplace(context.Background(), "apm-demo")
		if !errors.Is(err, ErrPluginStateUnknown) {
			t.Fatalf("case %d error = %v, want ErrPluginStateUnknown", index, err)
		}
	}
}

func TestClaudePluginMarketplaceMutationCommands(t *testing.T) {
	source := "/abs/cache/plugins/demo/v0.1.0"
	target := MarketplaceTarget{Name: "apm-demo", Source: source}
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{
		successfulMCPResponse(""),
		successfulMCPResponse(""),
	}}
	adapter := newClaudePluginAdapter(runner)
	if result, err := adapter.RegisterMarketplace(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("RegisterMarketplace() = %#v, %v", result, err)
	}
	if result, err := adapter.UnregisterMarketplace(context.Background(), target); err != nil || result.State != PluginMutationApplied {
		t.Fatalf("UnregisterMarketplace() = %#v, %v", result, err)
	}
	assertPluginCommands(t, runner, "claude", [][]string{
		{"plugin", "marketplace", "add", source, "--scope", "user"},
		{"plugin", "marketplace", "remove", "apm-demo", "--scope", "user"},
	})
}

func TestClaudePluginMarketplaceMutationsClassifyRunnerFailures(t *testing.T) {
	target := MarketplaceTarget{Name: "apm-demo", Source: "/abs/cache/plugins/demo/v0.1.0"}
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
			result, err := newClaudePluginAdapter(runner).UnregisterMarketplace(context.Background(), target)
			if result.State != test.wantState || !errors.Is(err, test.wantErr) {
				t.Fatalf("UnregisterMarketplace() = %#v, %v; want %q, %v", result, err, test.wantState, test.wantErr)
			}
		})
	}
}

func TestClaudePluginInspectAbsentAndAmbiguous(t *testing.T) {
	absentRunner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(`[]`)}}
	fact, err := newClaudePluginAdapter(absentRunner).Inspect(context.Background(), "demo-plugin")
	if err != nil || fact != (PluginFact{Presence: PluginAbsent, ActualName: "demo-plugin"}) {
		t.Fatalf("absent Inspect() = %#v, %v", fact, err)
	}

	ambiguousRunner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(
		`[{"id":"demo-plugin@one"},{"id":"demo-plugin@two"}]`,
	)}}
	if _, err := newClaudePluginAdapter(ambiguousRunner).Inspect(context.Background(), "demo-plugin"); !errors.Is(err, ErrPluginStateUnknown) {
		t.Fatalf("ambiguous Inspect() error = %v", err)
	}
}

func TestClaudePluginInspectReportsSingleForeignSelector(t *testing.T) {
	runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(
		`[{"id":"demo-plugin@foreign"}]`,
	)}}
	fact, err := newClaudePluginAdapter(runner).Inspect(context.Background(), "demo-plugin")
	want := PluginFact{Presence: PluginPresent, ActualName: "demo-plugin", Selector: "demo-plugin@foreign"}
	if err != nil || fact != want {
		t.Fatalf("Inspect() = %#v, %v; want %#v", fact, err, want)
	}
}

func TestClaudePluginInspectRejectsMalformedTopLevelCollection(t *testing.T) {
	// 捕获错误变更：missing/null collection 被当成明确空列表，从而把未知状态误报为 absent。
	for _, document := range []string{`{}`, `{"installed":null}`, `null`} {
		runner := &recordingMCPRunner{responses: []queuedMCPResponse{successfulMCPResponse(document)}}
		_, err := newClaudePluginAdapter(runner).Inspect(context.Background(), "demo-plugin")
		if !errors.Is(err, ErrPluginStateUnknown) {
			t.Fatalf("Inspect(%s) error = %v, want ErrPluginStateUnknown", document, err)
		}
	}
}
