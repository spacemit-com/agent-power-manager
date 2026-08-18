package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

func TestUninstallPluginWithoutManifestSkipsAllPluginDependencies(t *testing.T) {
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		snapshot: domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex}},
		groups:   map[domain.AgentID]domain.PowerGroups{domain.AgentCodex: {}},
	})

	result, err := service.Uninstall(context.Background(), cli.Flags{Agent: "codex", Type: "plugin", Name: "demo"})
	if err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Outcome != cli.UninstallOutcomeUnexecuted {
		t.Fatalf("Uninstall() = %+v, want manifest-missing unexecuted", result)
	}
	want := []string{"acquire", "recover", "refresh", "config:codex", "release"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want manifest-only %v", calls, want)
	}
}

func TestUninstallPluginMarketplaceDoubleAbsentCleansOnlyManifest(t *testing.T) {
	var calls []string
	ref := pluginRef("v1.0.0")
	service := newUninstallService(t, &calls, uninstallFixture{pluginMode: domain.PluginDeploymentMarketplace})

	result, err := service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
	if err != nil || !result.TargetMissing {
		t.Fatalf("uninstallPlugin() = %+v, %v, want target missing", result, err)
	}
	want := []string{
		"resolve-plugin:codex", "marketplace:inspect:codex:apm-demo", "plugin:inspect:codex:demo-v1.0.0",
		"config:remove:codex:plugin:demo",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestUninstallPluginMarketplaceExactManagedStateUsesOfflineLookup(t *testing.T) {
	var calls []string
	var requests []transaction.PluginUninstallRequest
	ref := pluginRef("v1.0.0")
	service := newUninstallService(t, &calls, uninstallFixture{
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"},
		},
		pluginFacts: map[string]adapter.PluginFact{
			"demo-v1.0.0": {Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"},
		},
		pluginRequestSink: &requests,
	})

	result, err := service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
	if err != nil || result.TargetMissing {
		t.Fatalf("uninstallPlugin() = %+v, %v, want transaction", result, err)
	}
	want := []string{
		"resolve-plugin:codex", "marketplace:inspect:codex:apm-demo", "plugin:inspect:codex:demo-v1.0.0",
		"resource:lookup:codex:v1.0.0", "transaction:uninstall-plugin:codex",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if len(requests) != 1 || requests[0].Target != (adapter.PluginTarget{ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}) || requests[0].Marketplace == nil || requests[0].Marketplace.Source != "/cache/plugins/codex/demo/v1.0.0" {
		t.Fatalf("requests = %+v, want exact offline artifact", requests)
	}
}

func TestUninstallPluginMarketplaceFailsClosedOnAnomalies(t *testing.T) {
	ref := pluginRef("v1.0.0")
	cacheDamage := errors.New("cached artifact damaged")
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
		lookupErr       error
		wantErr         error
	}{
		{name: "marketplace only", marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"}, pluginFact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo-v1.0.0"}, wantErr: transaction.ErrIndeterminateState},
		{name: "plugin only", marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"}, pluginFact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}, wantErr: transaction.ErrIndeterminateState},
		{name: "wrong source", marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/other"}, pluginFact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}, wantErr: transaction.ErrIndeterminateState},
		{name: "damaged cache", marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"}, pluginFact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}, lookupErr: cacheDamage, wantErr: cacheDamage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			resources := &fakePluginResources{lookupErr: map[string]error{pluginArtifactKey(domain.AgentCodex, ref): test.lookupErr}}
			service := newUninstallService(t, &calls, uninstallFixture{
				pluginMode:       domain.PluginDeploymentMarketplace,
				marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:      map[string]adapter.PluginFact{"demo-v1.0.0": test.pluginFact},
				pluginResources:  resources,
			})
			_, err := service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("uninstallPlugin() error = %v, want %v", err, test.wantErr)
			}
			if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:uninstall-plugin:codex") {
				t.Fatalf("calls = %v, anomaly must preserve manifest", calls)
			}
		})
	}
}

func TestUninstallPluginUnknownCollectionFactRetainsManifestWithoutMutation(t *testing.T) {
	// 捕获错误变更：malformed Plugin list 的 unknown 状态被当成 absent 并删除已安装 manifest。
	ref := pluginRef("v1.0.0")
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	service := newUninstallService(t, &calls, uninstallFixture{
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"},
		},
		pluginInspectErrors: map[string]error{actualName: adapter.ErrPluginStateUnknown},
	})

	_, err = service.uninstallPlugin(context.Background(), domain.AgentCodex, ref)
	if !errors.Is(err, adapter.ErrPluginStateUnknown) {
		t.Fatalf("uninstallPlugin() error = %v, want ErrPluginStateUnknown", err)
	}
	if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:uninstall-plugin:codex") {
		t.Fatalf("calls = %v, unknown fact must retain manifest and Agent state", calls)
	}
}

func TestUninstallPluginHermesUsesPluginFactWithoutArtifactLookup(t *testing.T) {
	ref := pluginRef("v1.0.0")
	tests := []struct {
		name        string
		fact        adapter.PluginFact
		wantMissing bool
		wantTxn     bool
	}{
		{name: "absent cleans manifest", fact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo-v1.0.0"}, wantMissing: true},
		{name: "present uninstalls", fact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0"}, wantTxn: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			resources := &fakePluginResources{}
			service := newUninstallService(t, &calls, uninstallFixture{
				pluginMode:      domain.PluginDeploymentFilesystem,
				pluginFacts:     map[string]adapter.PluginFact{"demo-v1.0.0": test.fact},
				pluginResources: resources,
			})
			result, err := service.uninstallPlugin(context.Background(), domain.AgentHermes, ref)
			if err != nil || result.TargetMissing != test.wantMissing {
				t.Fatalf("uninstallPlugin() = %+v, %v", result, err)
			}
			if containsEvent(calls, "transaction:uninstall-plugin:hermes-agent") != test.wantTxn || resources.lookupCalls != 0 || resources.ensureCalls != 0 {
				t.Fatalf("calls = %v, lookup/ensure=%d/%d", calls, resources.lookupCalls, resources.ensureCalls)
			}
		})
	}
}
