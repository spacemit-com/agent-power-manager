package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
)

func TestUninstallPluginDispatchesThroughPluginAdapterOnly(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups: map[domain.AgentID]domain.InstalledPowerGroups{
			domain.AgentCodex: {domain.PowerTypePlugin: {domain.InstalledPower{Ref: ref}}},
		},
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
		},
		pluginFact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo"})
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Power: powerPointer(ref), Outcome: cli.UninstallOutcomeCompleted}})
	if containsEvent(calls, "resolve:codex") || containsEvent(calls, "transaction:uninstall:codex") {
		t.Fatalf("calls = %v, want no Skill adapter or filesystem transaction", calls)
	}
}

func TestUninstallConfigFailureReturnsAgentOnlyFailedAndLaterUnexecutedItems(t *testing.T) {
	cause := errors.New("read manifest")
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot:     domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex}},
		configErrors: map[domain.AgentID]error{domain.AgentClaudeCode: cause},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "all", Name: "demo"})
	assertUninstallItems(t, result, []cli.UninstallItem{
		{Agent: domain.AgentClaudeCode, Outcome: cli.UninstallOutcomeFailed},
		{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted},
	})
	assertUninstallBatchError(t, err, domain.AgentClaudeCode, cause)
	wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "release"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestUninstallMissingRecordWarnsSynchronouslyWithOptionalType(t *testing.T) {
	tests := []struct {
		name     string
		flags    cli.Flags
		wantType string
	}{
		{name: "without type", flags: cli.Flags{Agent: "codex", Name: "Demo"}},
		{name: "with type", flags: cli.Flags{Agent: "codex", Type: "plugin", Name: "Demo"}, wantType: "type=plugin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			uninstallTestLogger(t, &stdout, &stderr, false)
			fixture := uninstallFixture{snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}}}
			fixture.beforeRelease = func() {
				assertContainsAll(t, stderr.String(), []string{
					`msg="power is not installed"`, "operation=uninstall", "agent=codex", "power=Demo",
				})
			}
			var calls []string
			service := newUninstallService(t, &calls, fixture)

			result, err := service.Uninstall(context.Background(), tt.flags)
			if err != nil {
				t.Fatal(err)
			}
			assertUninstallItems(t, result, []cli.UninstallItem{{Agent: domain.AgentCodex, Outcome: cli.UninstallOutcomeUnexecuted}})
			if strings.Count(stderr.String(), "level=WARN") != 1 {
				t.Fatalf("stderr = %q, want exactly one Warn", stderr.String())
			}
			if tt.wantType == "" && strings.Contains(stderr.String(), "type=") {
				t.Fatalf("stderr = %q, want no type attribute", stderr.String())
			}
			if tt.wantType != "" && !strings.Contains(stderr.String(), tt.wantType) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantType)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
