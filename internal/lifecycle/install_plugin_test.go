package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"apm/internal/adapter"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/pluginresource"
	"apm/internal/transaction"
)

func TestInstallPluginManifestTargetIsUnchangedWithoutDependencies(t *testing.T) {
	var calls []string
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}
	service := newInstallService(t, &calls, installFixture{
		installed: map[domain.AgentID]domain.PowerRef{domain.AgentCodex: ref},
	})

	item, err := service.installForAgent(context.Background(), domain.AgentCodex, ref)
	if err != nil {
		t.Fatalf("installForAgent() error = %v", err)
	}
	if item != (cli.InstallItem{Agent: domain.AgentCodex, Power: ref, Outcome: cli.InstallOutcomeUnchanged}) {
		t.Fatalf("installForAgent() = %+v, want unchanged", item)
	}
	if want := []string{"config:find:codex"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want manifest-only %v", calls, want)
	}
}

func TestInstallPluginMarketplaceFirstInstallUsesLocalArtifact(t *testing.T) {
	var calls []string
	var requests []transaction.PluginInstallRequest
	ref := pluginRef("v2.0.0")
	service := newInstallService(t, &calls, installFixture{
		pluginMode:        domain.PluginDeploymentMarketplace,
		pluginRequestSink: &requests,
	})

	item, err := service.installForAgent(context.Background(), domain.AgentCodex, ref)
	if err != nil {
		t.Fatalf("installForAgent() error = %v", err)
	}
	if item.Outcome != cli.InstallOutcomeInstalled {
		t.Fatalf("installForAgent() outcome = %q, want installed", item.Outcome)
	}
	wantCalls := []string{
		"config:find:codex",
		"resource:ensure:codex:v2.0.0",
		"resolve-plugin:codex",
		"marketplace:inspect:codex:apm-demo",
		"plugin:inspect:codex:demo-v2.0.0",
		"transaction:install-plugin:codex",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.Previous != nil || request.Target != (adapter.PluginTarget{ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0@apm-demo"}) {
		t.Fatalf("request = %+v, want exact first-install target", request)
	}
	if request.Marketplace == nil || request.Marketplace.Target != (adapter.MarketplaceTarget{Name: "apm-demo", Source: "/cache/plugins/codex/demo/v2.0.0"}) || request.Marketplace.Previous != nil {
		t.Fatalf("marketplace request = %+v, want target local artifact", request.Marketplace)
	}
}

func TestInstallPluginMarketplaceRejectsUnmanagedFacts(t *testing.T) {
	ref := pluginRef("v2.0.0")
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
		wantErr         error
	}{
		{
			name:            "marketplace exists",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/external"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo-v2.0.0"},
			wantErr:         adapter.ErrMarketplaceConflict,
		},
		{
			name:            "plugin exists",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0@external"},
			wantErr:         adapter.ErrPluginConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			service := newInstallService(t, &calls, installFixture{
				pluginMode:       domain.PluginDeploymentMarketplace,
				marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:      map[string]adapter.PluginFact{"demo-v2.0.0": test.pluginFact},
			})
			_, err := service.installForAgent(context.Background(), domain.AgentCodex, ref)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("installForAgent() error = %v, want %v", err, test.wantErr)
			}
			if containsEvent(calls, "transaction:install-plugin:codex") {
				t.Fatalf("calls = %v, unmanaged facts must not enter transaction", calls)
			}
		})
	}
}

func TestInstallPluginMarketplaceSwitchUsesPreviousLookupOnly(t *testing.T) {
	var calls []string
	var requests []transaction.PluginInstallRequest
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	service := newInstallService(t, &calls, installFixture{
		installed:         map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous},
		pluginMode:        domain.PluginDeploymentMarketplace,
		pluginRequestSink: &requests,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"},
		},
		pluginFacts: map[string]adapter.PluginFact{
			"demo-v1.0.0": {Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"},
		},
	})

	item, err := service.installForAgent(context.Background(), domain.AgentCodex, target)
	if err != nil {
		t.Fatalf("installForAgent() error = %v", err)
	}
	if item.Outcome != cli.InstallOutcomeUpgraded {
		t.Fatalf("outcome = %q, want upgraded", item.Outcome)
	}
	wantCalls := []string{
		"config:find:codex",
		"resource:ensure:codex:v2.0.0",
		"resolve-plugin:codex",
		"marketplace:inspect:codex:apm-demo",
		"plugin:inspect:codex:demo-v1.0.0",
		"resource:lookup:codex:v1.0.0",
		"transaction:install-plugin:codex",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if len(requests) != 1 || requests[0].Previous == nil || *requests[0].Previous != previous || requests[0].PreviousTarget == nil || requests[0].PreviousTarget.ActualName != "demo-v1.0.0" {
		t.Fatalf("requests = %+v, want exact previous targets", requests)
	}
	if requests[0].Marketplace == nil || requests[0].Marketplace.Previous == nil || requests[0].Marketplace.Previous.Source != "/cache/plugins/codex/demo/v1.0.0" {
		t.Fatalf("marketplace request = %+v, want previous cached source", requests[0].Marketplace)
	}
}

func TestInstallPluginMarketplaceStaleManifestClearsSingleEntryThenFirstInstalls(t *testing.T) {
	var calls []string
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	service := newInstallService(t, &calls, installFixture{
		installed:  map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous},
		pluginMode: domain.PluginDeploymentMarketplace,
	})

	item, err := service.installForAgent(context.Background(), domain.AgentCodex, target)
	if err != nil || item.Outcome != cli.InstallOutcomeInstalled {
		t.Fatalf("installForAgent() = %+v, %v, want fresh install", item, err)
	}
	wantCalls := []string{
		"config:find:codex", "resource:ensure:codex:v2.0.0", "resolve-plugin:codex",
		"marketplace:inspect:codex:apm-demo", "plugin:inspect:codex:demo-v1.0.0",
		"config:remove:codex:plugin:demo",
		"marketplace:inspect:codex:apm-demo", "plugin:inspect:codex:demo-v2.0.0",
		"transaction:install-plugin:codex",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestInstallPluginMarketplaceDoubleAbsentWrongMarketplaceNamePreservesManifest(t *testing.T) {
	var calls []string
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	service := newInstallService(t, &calls, installFixture{
		installed:  map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous},
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplaceAbsent, Name: "apm-other"},
		},
	})

	_, err := service.installForAgent(context.Background(), domain.AgentCodex, target)
	if !errors.Is(err, transaction.ErrIndeterminateState) {
		t.Fatalf("installForAgent() error = %v, want ErrIndeterminateState", err)
	}
	assertPluginManifestPreservedWithoutMutation(t, calls)
}

func TestInstallPluginMarketplaceDoubleAbsentWrongPluginNamePreservesManifest(t *testing.T) {
	var calls []string
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	service := newInstallService(t, &calls, installFixture{
		installed:  map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous},
		pluginMode: domain.PluginDeploymentMarketplace,
		pluginFacts: map[string]adapter.PluginFact{
			"demo-v1.0.0": {Presence: adapter.PluginAbsent, ActualName: "other-v1.0.0"},
		},
	})

	_, err := service.installForAgent(context.Background(), domain.AgentCodex, target)
	if !errors.Is(err, transaction.ErrIndeterminateState) {
		t.Fatalf("installForAgent() error = %v, want ErrIndeterminateState", err)
	}
	assertPluginManifestPreservedWithoutMutation(t, calls)
}

func assertPluginManifestPreservedWithoutMutation(t *testing.T, calls []string) {
	t.Helper()
	for _, forbidden := range []string{
		"config:remove:codex:plugin:demo",
		"resource:stage-hermes:v2.0.0",
		"transaction:install-plugin:codex",
		"cloud:get-plugin",
		"cloud:download-plugin",
	} {
		if containsEvent(calls, forbidden) {
			t.Fatalf("calls = %v, want no %q", calls, forbidden)
		}
	}
}

func TestInstallPluginMarketplaceFailsClosedOnManagedAnomalies(t *testing.T) {
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	cacheDamage := errors.New("old artifact damaged")
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
		{name: "damaged old cache", marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"}, pluginFact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}, lookupErr: cacheDamage, wantErr: cacheDamage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			resources := &fakePluginResources{lookupErr: map[string]error{pluginArtifactKey(domain.AgentCodex, previous): test.lookupErr}}
			service := newInstallService(t, &calls, installFixture{
				installed: map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous}, pluginMode: domain.PluginDeploymentMarketplace,
				marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:      map[string]adapter.PluginFact{"demo-v1.0.0": test.pluginFact},
				pluginResources:  resources,
			})
			_, err := service.installForAgent(context.Background(), domain.AgentCodex, target)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("installForAgent() error = %v, want %v", err, test.wantErr)
			}
			if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:install-plugin:codex") {
				t.Fatalf("calls = %v, anomaly must preserve manifest and avoid transaction", calls)
			}
		})
	}
}

func TestInstallPluginHermesStagesOnlyAtPublishBoundary(t *testing.T) {
	ref := pluginRef("v2.0.0")
	tests := []struct {
		name      string
		fact      adapter.PluginFact
		wantErr   error
		wantStage int
		wantTxn   bool
	}{
		{name: "absent publishes", fact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo-v2.0.0"}, wantStage: 1, wantTxn: true},
		{name: "unmanaged present conflicts", fact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo-v2.0.0", Selector: "demo-v2.0.0"}, wantErr: adapter.ErrPluginConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			resources := &fakePluginResources{}
			service := newInstallService(t, &calls, installFixture{
				pluginMode:      domain.PluginDeploymentFilesystem,
				pluginFacts:     map[string]adapter.PluginFact{"demo-v2.0.0": test.fact},
				pluginResources: resources,
			})
			_, err := service.installForAgent(context.Background(), domain.AgentHermes, ref)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("installForAgent() error = %v, want %v", err, test.wantErr)
			}
			if resources.stageCalls != test.wantStage || containsEvent(calls, "transaction:install-plugin:hermes-agent") != test.wantTxn {
				t.Fatalf("calls = %v, stage=%d; want stage=%d transaction=%t", calls, resources.stageCalls, test.wantStage, test.wantTxn)
			}
		})
	}
}

func TestInstallPluginPreviousLookupNeverDownloadsOldVersion(t *testing.T) {
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	oldMiss := errors.New("offline old cache miss")
	resources := &fakePluginResources{lookupErr: map[string]error{pluginArtifactKey(domain.AgentCodex, previous): oldMiss}}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		installed: map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous}, pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"}},
		pluginFacts:      map[string]adapter.PluginFact{"demo-v1.0.0": {Presence: adapter.PluginPresent, ActualName: "demo-v1.0.0", Selector: "demo-v1.0.0@apm-demo"}},
		pluginResources:  resources,
	})

	_, err := service.installForAgent(context.Background(), domain.AgentCodex, target)
	if !errors.Is(err, oldMiss) {
		t.Fatalf("installForAgent() error = %v, want old Lookup miss", err)
	}
	if resources.ensureCalls != 1 || resources.lookupCalls != 1 {
		t.Fatalf("Ensure/Lookup calls = %d/%d, want target Ensure once and previous Lookup once", resources.ensureCalls, resources.lookupCalls)
	}
}

func TestInstallPluginUnknownCollectionFactRetainsManifestWithoutMutation(t *testing.T) {
	// 捕获错误变更：Adapter 将 malformed collection 报为 unknown 后，lifecycle 仍清理 manifest 或开始事务。
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	previousName, err := domain.ActualPluginName(previous)
	if err != nil {
		t.Fatal(err)
	}
	unknown := adapter.ErrPluginStateUnknown
	resources := &fakePluginResources{}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		installed:  map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous},
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/codex/demo/v1.0.0"},
		},
		pluginInspectErrors: map[string]error{previousName: unknown},
		pluginResources:     resources,
	})

	_, err = service.installForAgent(context.Background(), domain.AgentCodex, target)
	if !errors.Is(err, unknown) {
		t.Fatalf("installForAgent() error = %v, want %v", err, unknown)
	}
	if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:install-plugin:codex") {
		t.Fatalf("calls = %v, unknown fact must retain manifest and Agent state", calls)
	}
	if resources.ensureCalls != 1 || resources.lookupCalls != 0 || resources.stageCalls != 0 {
		t.Fatalf("resource calls ensure/lookup/stage = %d/%d/%d, want 1/0/0", resources.ensureCalls, resources.lookupCalls, resources.stageCalls)
	}
}

func pluginRef(version string) domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: version}
}

var _ pluginresource.Manager = (*fakePluginResources)(nil)
