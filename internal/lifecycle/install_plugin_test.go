package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

	item, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, ref)
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

	item, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, ref)
	if err != nil {
		t.Fatalf("installForAgent() error = %v", err)
	}
	if item.Outcome != cli.InstallOutcomeInstalled {
		t.Fatalf("installForAgent() outcome = %q, want installed", item.Outcome)
	}
	wantCalls := []string{
		"config:find:codex",
		"resolve-plugin:codex",
		"resource:locate:v2.0.0",
		"marketplace:inspect:codex:apm-demo",
		"plugin:inspect:codex:demo",
		"resource:exists:v2.0.0",
		"resource:ensure:v2.0.0",
		"transaction:install-plugin:codex",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.Previous != nil || request.Target != (adapter.PluginTarget{ActualName: "demo", Selector: "demo@apm-demo"}) {
		t.Fatalf("request = %+v, want exact first-install target", request)
	}
	if request.Marketplace == nil || request.Marketplace.Target != (adapter.MarketplaceTarget{Name: "apm-demo", Source: "/cache/plugins/demo/v2.0.0"}) || request.Marketplace.Previous != nil {
		t.Fatalf("marketplace request = %+v, want target local artifact", request.Marketplace)
	}
}

func TestInstallPluginMarketplaceRejectsUnmanagedFacts(t *testing.T) {
	ref := pluginRef("v2.0.0")
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
	}{
		{
			name:            "marketplace exists",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/external"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
		},
		{
			name:            "plugin exists",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@external"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			service := newInstallService(t, &calls, installFixture{
				pluginMode:       domain.PluginDeploymentMarketplace,
				marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:      map[string]adapter.PluginFact{"demo": test.pluginFact},
			})
			_, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, ref)
			if !errors.Is(err, adapter.ErrTargetConflict) {
				t.Fatalf("installForAgent() error = %v, want ErrTargetConflict", err)
			}
			if containsEvent(calls, "transaction:install-plugin:codex") {
				t.Fatalf("calls = %v, unmanaged facts must not enter transaction", calls)
			}
		})
	}
}

func TestInstallPluginMarketplaceClaudeForeignSelectorFailsClosed(t *testing.T) {
	var calls []string
	ref := pluginRef("v2.0.0")
	service := newInstallService(t, &calls, installFixture{
		pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{
			"apm-demo": {Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
		},
		pluginFacts: map[string]adapter.PluginFact{
			"demo": {Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@foreign"},
		},
	})

	_, err := installForAgentTest(t, service, context.Background(), domain.AgentClaudeCode, ref)
	if !errors.Is(err, adapter.ErrTargetConflict) {
		t.Fatalf("installForAgent() error = %v, want ErrTargetConflict", err)
	}
	if containsEvent(calls, "transaction:install-plugin:"+string(domain.AgentClaudeCode)) {
		t.Fatalf("calls = %v, foreign selector must not enter transaction", calls)
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
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
		},
		pluginFacts: map[string]adapter.PluginFact{
			"demo": {Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
		},
	})

	item, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
	if err != nil {
		t.Fatalf("installForAgent() error = %v", err)
	}
	if item.Outcome != cli.InstallOutcomeUpgraded {
		t.Fatalf("outcome = %q, want upgraded", item.Outcome)
	}
	wantCalls := []string{
		"config:find:codex",
		"resolve-plugin:codex",
		"resource:locate:v1.0.0",
		"marketplace:inspect:codex:apm-demo",
		"plugin:inspect:codex:demo",
		"resource:exists:v1.0.0",
		"resource:ensure:v2.0.0",
		"resource:lookup:v1.0.0",
		"transaction:install-plugin:codex",
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	if len(requests) != 1 || requests[0].Previous == nil || *requests[0].Previous != previous || requests[0].PreviousTarget == nil || requests[0].PreviousTarget.ActualName != "demo" {
		t.Fatalf("requests = %+v, want exact previous targets", requests)
	}
	if requests[0].Marketplace == nil || requests[0].Marketplace.Previous == nil || requests[0].Marketplace.Previous.Source != "/cache/plugins/demo/v1.0.0" {
		t.Fatalf("marketplace request = %+v, want previous cached source", requests[0].Marketplace)
	}
}

func TestInstallPluginMarketplaceRepairsThenFirstInstalls(t *testing.T) {
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	tests := []struct {
		name            string
		marketplaceFact adapter.MarketplaceFact
		pluginFact      adapter.PluginFact
	}{
		{
			name:            "both absent",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
		},
		{
			name:            "marketplace only",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"},
		},
		{
			name:            "plugin only",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplaceAbsent, Name: "apm-demo"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			var uninstalls []transaction.PluginUninstallRequest
			service := newInstallService(t, &calls, installFixture{
				installed:           map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous},
				pluginMode:          domain.PluginDeploymentMarketplace,
				marketplaceFacts:    map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:         map[string]adapter.PluginFact{"demo": test.pluginFact},
				pluginUninstallSink: &uninstalls,
			})

			item, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
			if err != nil || item.Outcome != cli.InstallOutcomeInstalled {
				t.Fatalf("installForAgent() = %+v, %v, want fresh install after repair", item, err)
			}
			wantCalls := []string{
				"config:find:codex", "resolve-plugin:codex", "resource:locate:v1.0.0",
				"marketplace:inspect:codex:apm-demo", "plugin:inspect:codex:demo", "resource:exists:v1.0.0",
				"transaction:uninstall-plugin:codex", "resource:ensure:v2.0.0", "transaction:install-plugin:codex",
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, wantCalls)
			}
			if len(uninstalls) != 1 || uninstalls[0].Power != previous || uninstalls[0].Marketplace == nil || uninstalls[0].Marketplace.Source != "/cache/plugins/demo/v1.0.0" {
				t.Fatalf("uninstall requests = %+v, want previous marketplace repair", uninstalls)
			}
		})
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

	_, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
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
			"demo": {Presence: adapter.PluginAbsent, ActualName: "other"},
		},
	})

	_, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
	if !errors.Is(err, transaction.ErrIndeterminateState) {
		t.Fatalf("installForAgent() error = %v, want ErrIndeterminateState", err)
	}
	assertPluginManifestPreservedWithoutMutation(t, calls)
}

func assertPluginManifestPreservedWithoutMutation(t *testing.T, calls []string) {
	t.Helper()
	for _, forbidden := range []string{
		"config:remove:codex:plugin:demo",
		"resource:stage-filesystem:v2.0.0",
		"transaction:install-plugin:codex",
		"transaction:uninstall-plugin:codex",
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
		{
			name:            "wrong source",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/other"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
			wantErr:         transaction.ErrIndeterminateState,
		},
		{
			name:            "wrong selector",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@foreign"},
			wantErr:         transaction.ErrIndeterminateState,
		},
		{
			name:            "damaged old cache",
			marketplaceFact: adapter.MarketplaceFact{Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
			pluginFact:      adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"},
			lookupErr:       cacheDamage,
			wantErr:         cacheDamage,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			resources := &fakePluginResources{lookupErr: map[string]error{pluginArtifactKey(previous): test.lookupErr}}
			service := newInstallService(t, &calls, installFixture{
				installed: map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous}, pluginMode: domain.PluginDeploymentMarketplace,
				marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": test.marketplaceFact},
				pluginFacts:      map[string]adapter.PluginFact{"demo": test.pluginFact},
				pluginResources:  resources,
			})
			_, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("installForAgent() error = %v, want %v", err, test.wantErr)
			}
			if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:install-plugin:codex") {
				t.Fatalf("calls = %v, anomaly must preserve manifest and avoid transaction", calls)
			}
		})
	}
}

func TestInstallFilesystemPluginStagesOnlyAtPublishBoundaryForBibitAndHermes(t *testing.T) {
	ref := pluginRef("v2.0.0")
	tests := []struct {
		name      string
		fact      adapter.PluginFact
		wantErr   error
		wantStage int
		wantTxn   bool
	}{
		{name: "absent publishes", fact: adapter.PluginFact{Presence: adapter.PluginAbsent, ActualName: "demo"}, wantStage: 1, wantTxn: true},
		{name: "unmanaged present conflicts", fact: adapter.PluginFact{Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo"}, wantErr: adapter.ErrTargetConflict},
	}
	for _, agent := range []domain.AgentID{domain.AgentBibit, domain.AgentHermes} {
		for _, test := range tests {
			t.Run(string(agent)+"/"+test.name, func(t *testing.T) {
				var calls []string
				resources := &fakePluginResources{}
				target := filepath.Join(t.TempDir(), "demo")
				service := newInstallService(t, &calls, installFixture{
					pluginMode:      domain.PluginDeploymentFilesystem,
					pluginFacts:     map[string]adapter.PluginFact{"demo": test.fact},
					pluginTargets:   map[string]string{"demo": target},
					pluginResources: resources,
				})
				_, err := installForAgentTest(t, service, context.Background(), agent, ref)
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("installForAgent() error = %v, want %v", err, test.wantErr)
				}
				if resources.stageCalls != test.wantStage || containsEvent(calls, "transaction:install-plugin:"+string(agent)) != test.wantTxn {
					t.Fatalf("calls = %v, stage=%d; want stage=%d transaction=%t", calls, resources.stageCalls, test.wantStage, test.wantTxn)
				}
			})
		}
	}
}

func TestInstallFilesystemPluginAbsentFactValidatesPhysicalManagedTargetBeforeMutation(t *testing.T) {
	previous := pluginRef("v1.0.0")
	targetRef := pluginRef("v2.0.0")
	tests := []struct {
		name    string
		create  func(*testing.T, string)
		wantErr error
	}{
		{name: "physical directory", create: func(t *testing.T, target string) {
			t.Helper()
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}, wantErr: transaction.ErrIndeterminateState},
		{name: "symlink", create: func(t *testing.T, target string) {
			t.Helper()
			if err := os.Symlink(t.TempDir(), target); err != nil {
				t.Fatal(err)
			}
		}, wantErr: transaction.ErrUnsafePath},
		{name: "regular file", create: func(t *testing.T, target string) {
			t.Helper()
			if err := os.WriteFile(target, []byte("foreign"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, wantErr: transaction.ErrUnsafePath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "demo")
			test.create(t, target)
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			var calls []string
			resources := &fakePluginResources{}
			service := newInstallService(t, &calls, installFixture{
				installed:       map[domain.AgentID]domain.PowerRef{domain.AgentHermes: previous},
				pluginMode:      domain.PluginDeploymentFilesystem,
				pluginFacts:     map[string]adapter.PluginFact{"demo": {Presence: adapter.PluginAbsent, ActualName: "demo"}},
				pluginTargets:   map[string]string{"demo": target},
				pluginResources: resources,
			})

			_, err = installForAgentTest(t, service, context.Background(), domain.AgentHermes, targetRef)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("installForAgent() error = %v, want %v", err, test.wantErr)
			}
			after, statErr := os.Lstat(target)
			if statErr != nil || after.Mode() != before.Mode() {
				t.Fatalf("managed target changed: before=%v after=%v err=%v", before.Mode(), after, statErr)
			}
			for _, forbidden := range []string{"config:remove:hermes-agent:plugin:demo", "resource:ensure:v2.0.0", "resource:stage-filesystem:hermes-agent:v2.0.0", "transaction:install-plugin:hermes-agent"} {
				if containsEvent(calls, forbidden) {
					t.Fatalf("calls = %v, want no mutation %q", calls, forbidden)
				}
			}
		})
	}
}

func TestInstallFilesystemPluginAbsentFactAndPhysicalTargetMissingRepairsThenInstalls(t *testing.T) {
	previous := pluginRef("v1.0.0")
	targetRef := pluginRef("v2.0.0")
	target := filepath.Join(t.TempDir(), "demo")
	var calls []string
	var uninstalls []transaction.PluginUninstallRequest
	service := newInstallService(t, &calls, installFixture{
		installed:           map[domain.AgentID]domain.PowerRef{domain.AgentHermes: previous},
		pluginMode:          domain.PluginDeploymentFilesystem,
		pluginFacts:         map[string]adapter.PluginFact{"demo": {Presence: adapter.PluginAbsent, ActualName: "demo"}},
		pluginTargets:       map[string]string{"demo": target},
		pluginUninstallSink: &uninstalls,
	})

	item, err := installForAgentTest(t, service, context.Background(), domain.AgentHermes, targetRef)
	if err != nil || item.Outcome != cli.InstallOutcomeInstalled {
		t.Fatalf("installForAgent() = %+v, %v; want fresh install after repair", item, err)
	}
	want := []string{
		"config:find:hermes-agent", "resolve-plugin:hermes-agent", "plugin:inspect:hermes-agent:demo",
		"plugin:target-path:hermes-agent:demo", "transaction:uninstall-plugin:hermes-agent",
		"resource:ensure:v2.0.0", "resource:stage-filesystem:hermes-agent:v2.0.0",
		"transaction:install-plugin:hermes-agent",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if len(uninstalls) != 1 || uninstalls[0].Power != previous || uninstalls[0].Marketplace != nil {
		t.Fatalf("uninstall requests = %+v, want filesystem previous repair", uninstalls)
	}
}

func TestInstallPluginPreviousLookupNeverDownloadsOldVersion(t *testing.T) {
	previous := pluginRef("v1.0.0")
	target := pluginRef("v2.0.0")
	oldMiss := errors.New("offline old cache miss")
	resources := &fakePluginResources{lookupErr: map[string]error{pluginArtifactKey(previous): oldMiss}}
	var calls []string
	service := newInstallService(t, &calls, installFixture{
		installed: map[domain.AgentID]domain.PowerRef{domain.AgentCodex: previous}, pluginMode: domain.PluginDeploymentMarketplace,
		marketplaceFacts: map[string]adapter.MarketplaceFact{"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"}},
		pluginFacts:      map[string]adapter.PluginFact{"demo": {Presence: adapter.PluginPresent, ActualName: "demo", Selector: "demo@apm-demo"}},
		pluginResources:  resources,
	})

	_, err := installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
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
	previousName, err := domain.PluginRuntimeName(previous)
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
			"apm-demo": {Presence: adapter.MarketplacePresent, Name: "apm-demo", Source: "/cache/plugins/demo/v1.0.0"},
		},
		pluginInspectErrors: map[string]error{previousName: unknown},
		pluginResources:     resources,
	})

	_, err = installForAgentTest(t, service, context.Background(), domain.AgentCodex, target)
	if !errors.Is(err, unknown) {
		t.Fatalf("installForAgent() error = %v, want %v", err, unknown)
	}
	if containsEvent(calls, "config:remove:codex:plugin:demo") || containsEvent(calls, "transaction:install-plugin:codex") {
		t.Fatalf("calls = %v, unknown fact must retain manifest and Agent state", calls)
	}
	if resources.ensureCalls != 0 || resources.lookupCalls != 0 || resources.stageCalls != 0 {
		t.Fatalf("resource calls ensure/lookup/stage = %d/%d/%d, want 0/0/0", resources.ensureCalls, resources.lookupCalls, resources.stageCalls)
	}
}

func pluginRef(version string) domain.PowerRef {
	return domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: version}
}

var _ pluginresource.Manager = (*fakePluginResources)(nil)
var _ pluginresource.ArtifactLocator = (*fakePluginResources)(nil)
